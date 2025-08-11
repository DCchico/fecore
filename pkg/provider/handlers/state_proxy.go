package handlers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.gatech.edu/faasedge/fecore/pkg/semaphore"
)

// StateProxyHandler handles state management for remote functions
type StateProxyHandler struct {
	// Add any dependencies you need
}

// StateRequest represents the request body for state operations
type StateRequest struct {
	StateID string `json:"state_id"`
	VDAGID  string `json:"vdag_id"`
	FrameID int    `json:"frame_id"`
	Op      string `json:"op"` // "Get" or "Put"
	Data    []byte `json:"data,omitempty"`
}

// StateResponse represents the response for state operations
type StateResponse struct {
	Success bool   `json:"success"`
	Data    []byte `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NewStateProxyHandler creates a new state proxy handler
func NewStateProxyHandler() *StateProxyHandler {
	return &StateProxyHandler{}
}

// RegisterRoutes registers the state proxy routes
func (h *StateProxyHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/state", h.HandleStateRequest).Methods("GET", "PUT", "POST")
	router.HandleFunc("/state/{state_id}", h.HandleStateRequest).Methods("GET", "PUT")
}

// HandleStateRequest handles state get/put operations
func (h *StateProxyHandler) HandleStateRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req StateRequest
	var err error

	// Parse request based on method
	switch r.Method {
	case "GET":
		// For GET requests, parse from URL parameters or path
		vars := mux.Vars(r)
		if stateID, exists := vars["state_id"]; exists {
			req.StateID = stateID
		} else {
			req.StateID = r.URL.Query().Get("state_id")
		}
		req.VDAGID = r.URL.Query().Get("vdag_id")
		req.FrameID, _ = strconv.Atoi(r.URL.Query().Get("frame_id"))
		req.Op = r.URL.Query().Get("op")
		if req.Op == "" {
			req.Op = "Get" // Default to Get if not specified
		}
		
	case "PUT", "POST":
		// For PUT/POST requests, parse from JSON body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.sendErrorResponse(w, "Failed to read request body", err)
			return
		}
		
		if err := json.Unmarshal(body, &req); err != nil {
			h.sendErrorResponse(w, "Failed to parse JSON request", err)
			return
		}
		
		// For PUT requests with path parameter, override state_id
		if r.Method == "PUT" {
			vars := mux.Vars(r)
			if stateID, exists := vars["state_id"]; exists {
				req.StateID = stateID
			}
		}
	}

	// Validate request
	if err := h.validateRequest(&req); err != nil {
		// Return validation errors as JSON response with HTTP 200 (not 400)
		response := StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid request: %v", err),
		}
		responseJSON, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(responseJSON)
		return
	}

	// Handle the state operation
	var response StateResponse
	switch strings.ToLower(req.Op) {
	case "get":
		response, err = h.handleGetState(&req)
	case "put":
		response, err = h.handlePutState(&req)
	default:
		h.sendErrorResponse(w, "Invalid operation", fmt.Errorf("unknown operation: %s", req.Op))
		return
	}

	if err != nil {
		h.sendErrorResponse(w, "State operation failed", err)
		return
	}

	// Send response
	responseJSON, err := json.Marshal(response)
	if err != nil {
		h.sendErrorResponse(w, "Failed to marshal response", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseJSON)
}

// validateRequest validates the state request
func (h *StateProxyHandler) validateRequest(req *StateRequest) error {
	if req.StateID == "" {
		return fmt.Errorf("state_id is required")
	}
	if req.VDAGID == "" {
		return fmt.Errorf("vdag_id is required")
	}
	if req.FrameID < 0 {
		return fmt.Errorf("frame_id must be non-negative")
	}
	if req.Op == "" {
		return fmt.Errorf("op is required")
	}
	if strings.ToLower(req.Op) != "get" && strings.ToLower(req.Op) != "put" {
		return fmt.Errorf("op must be 'Get' or 'Put'")
	}
	return nil
}

// openSharedMemory opens or creates shared memory segment
func (h *StateProxyHandler) openSharedMemory(vdagID string, create bool) (int, error) {
	shmName := fmt.Sprintf("vt_camera_%s", vdagID)
	shmPath := fmt.Sprintf("/dev/shm/%s", shmName)
	
	var fd int
	var err error
	
	if create {
		// Create new shared memory segment using regular file operations
		fd, err = syscall.Open(shmPath, syscall.O_CREAT|syscall.O_RDWR, 0666)
		if err != nil {
			return -1, fmt.Errorf("failed to create shared memory: %v", err)
		}
		
		// Set size to 50MB
		if err := syscall.Ftruncate(fd, 50*1024*1024); err != nil {
			syscall.Close(fd)
			return -1, fmt.Errorf("failed to set shared memory size: %v", err)
		}
	} else {
		// Try to open existing shared memory
		fd, err = syscall.Open(shmPath, syscall.O_RDWR, 0666)
		if err != nil {
			return -1, fmt.Errorf("failed to open shared memory: %v", err)
		}
	}
	
	return fd, nil
}

// readSharedMemory reads raw data from shared memory using POSIX shared memory
func (h *StateProxyHandler) readSharedMemory(stateID string, vdagID string, frameID int) ([]byte, error) {
	log.Printf("[StateProxy] Reading shared memory for vdag_id=%s, frame_id=%d", vdagID, frameID)
	
	// Open existing shared memory segment (same as Python's SharedMemory(name=f"vt_camera_{camera_id}", create=False))
	shmName := fmt.Sprintf("%s_%s", stateID, vdagID)
	
	// Open the shared memory file descriptor
	fd, err := syscall.Open(fmt.Sprintf("/dev/shm/%s", shmName), syscall.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open shared memory %s: %v", shmName, err)
	}
	defer syscall.Close(fd)
	
	// Map the shared memory (equivalent to Python's shm.buf)
	// 50MB size like your Python code
	size := int64(50 * 1024 * 1024)
	
	// Map the memory
	addr, err := syscall.Mmap(fd, 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("failed to mmap shared memory: %v", err)
	}
	defer syscall.Munmap(addr)
	
	// Read header: [frame_id(8), state_size(8), valid_flag(8)] - same as your Python struct.pack("!QQQ", ...)
	if len(addr) < 24 {
		return nil, fmt.Errorf("shared memory too small for header")
	}
	
	// Parse header (big-endian like your Python code)
	currentFrame := binary.BigEndian.Uint64(addr[0:8])
	stateSize := binary.BigEndian.Uint64(addr[8:16])
	valid := binary.BigEndian.Uint64(addr[16:24])
	
	log.Printf("[StateProxy] Header: frame_id=%d, state_size=%d, valid=%d", currentFrame, stateSize, valid)
	
	// Check if data is valid and for the correct frame
	if valid != 1 {
		return nil, fmt.Errorf("shared memory data not valid (valid=%d)", valid)
	}
	
	if int(currentFrame) != frameID {
		return nil, fmt.Errorf("frame mismatch: expected %d, got %d", frameID, currentFrame)
	}
	
	// Read the actual data (skip 24-byte header)
	if len(addr) < 24+int(stateSize) {
		return nil, fmt.Errorf("shared memory too small for data")
	}
	
	data := make([]byte, stateSize)
	copy(data, addr[24:24+stateSize])
	
	log.Printf("[StateProxy] Successfully read %d bytes from shared memory", len(data))
	return data, nil
}

// writeSharedMemory writes raw data to shared memory using POSIX shared memory
func (h *StateProxyHandler) writeSharedMemory(stateID string, vdagID string, frameID int, data []byte) error {
	log.Printf("[StateProxy] Writing shared memory for state_id=%s, vdag_id=%s, frame_id=%d, data_size=%d", stateID, vdagID, frameID, len(data))
	
	// Open or create shared memory segment (same as Python's SharedMemory)
	shmName := fmt.Sprintf("%s_%s", stateID, vdagID)
	shmPath := fmt.Sprintf("/dev/shm/%s", shmName)
	
	var fd int
	var err error
	
	// Try to open existing first
	fd, err = syscall.Open(shmPath, syscall.O_RDWR, 0666)
	if err != nil {
		// Create new if doesn't exist
		fd, err = syscall.Open(shmPath, syscall.O_CREAT|syscall.O_RDWR, 0666)
		if err != nil {
			return fmt.Errorf("failed to create shared memory %s: %v", shmName, err)
		}
		
		// Set size to 50MB like your Python code
		if err := syscall.Ftruncate(fd, 50*1024*1024); err != nil {
			syscall.Close(fd)
			return fmt.Errorf("failed to set shared memory size: %v", err)
		}
	}
	defer syscall.Close(fd)
	
	// Map the shared memory
	size := int64(50 * 1024 * 1024)
	addr, err := syscall.Mmap(fd, 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("failed to mmap shared memory: %v", err)
	}
	defer syscall.Munmap(addr)
	
	// Check if data fits
	if len(addr) < 24+len(data) {
		return fmt.Errorf("shared memory too small for data")
	}
	
	// Write data first (like your Python code)
	copy(addr[24:24+len(data)], data)
	
	// Write header: [frame_id(8), state_size(8), valid_flag(8)] - same as your Python struct.pack("!QQQ", ...)
	binary.BigEndian.PutUint64(addr[0:8], uint64(frameID))
	binary.BigEndian.PutUint64(addr[8:16], uint64(len(data)))
	binary.BigEndian.PutUint64(addr[16:24], 1) // valid = 1
	
	log.Printf("[StateProxy] Successfully wrote %d bytes to shared memory", len(data))
	return nil
}

// handleGetState handles state retrieval with proper semaphore coordination
// READ Operation responsibilities:
// 1. Creates semaphore for current request
// 2. Checks previous request semaphore to see if data is ready
// 3. Cleans up previous semaphore after successful read
// 4. Handles state existence: If state doesn't exist (first request), returns empty/None
// 5. DOES NOT release current frame's semaphore - that's the WRITE operation's job
func (h *StateProxyHandler) handleGetState(req *StateRequest) (StateResponse, error) {
	log.Printf("[StateProxy] Getting state for state_id=%s, vdag_id=%s, frame_id=%d", 
		req.StateID, req.VDAGID, req.FrameID)

	// Check if state exists based on state name (state ID + VDAG ID), not sequence numbers
	stateName := fmt.Sprintf("%s_%s", req.StateID, req.VDAGID)
	shmPath := fmt.Sprintf("/dev/shm/%s", stateName)
	
	// If this is the first request and state doesn't exist, return empty/None
	if req.FrameID == 0 {
		if _, err := os.Stat(shmPath); os.IsNotExist(err) {
			log.Printf("[StateProxy] First request - state doesn't exist, returning empty")
			return StateResponse{
				Success: false,
				Error:   "No previous state exists for frame 0",
			}, nil
		}
	}

	// Construct semaphore name for the current frame
	currentFrameSemName := fmt.Sprintf("/%s_%s_%d_sem", req.StateID, req.VDAGID, req.FrameID)
	
	// Create semaphore for current request (READ responsibility #1)
	semCurrent, err := semaphore.Open(currentFrameSemName, semaphore.O_CREAT|semaphore.O_EXCL, 0666, 0)
	if err != nil {
		// If semaphore already exists, try to open it without O_EXCL
		if strings.Contains(err.Error(), "File exists") {
			semCurrent, err = semaphore.Open(currentFrameSemName, semaphore.O_RDWR, 0666, 0)
			if err != nil {
				return StateResponse{
					Success: false,
					Error:   fmt.Sprintf("Failed to open existing semaphore for current frame: %v", err),
				}, nil
			}
		} else {
			return StateResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to create semaphore for current frame: %v", err),
			}, nil
		}
	} else { _ = semCurrent }
	// Don't defer semCurrent.Close() here - we need to keep it open for PUT operations

	// For frame 0, just create the semaphore and return empty
	if req.FrameID == 0 {
		log.Printf("[StateProxy] Frame 0 requested - creating semaphore for coordination")
		// DO NOT release the semaphore here - it will be released by the PUT operation
		// Also, don't close the semaphore here - let the PUT operation grab it
		return StateResponse{
			Success: false,
			Error:   "No previous state exists for frame 0",
		}, nil
	}

	// For frame 1+, check previous request semaphore to see if data is ready (READ responsibility #2)
	prevFrameSemName := fmt.Sprintf("/%s_%s_%d_sem", req.StateID, req.VDAGID, req.FrameID-1)
	
	// Wait for semaphore to exist and be ready
	var semPrev *semaphore.Semaphore
	maxWaitSeconds := 10
	waitStart := time.Now()
	
	for {
		// Try to open existing semaphore for the previous frame
		semPrev, err = semaphore.Open(prevFrameSemName, semaphore.O_RDWR, 0666, 0)
		if err == nil {
			break // Semaphore exists, proceed
		}
		
		// Check if we've timed out
		if time.Since(waitStart) >= time.Duration(maxWaitSeconds)*time.Second {
			return StateResponse{
				Success: false, 
				Error:   fmt.Sprintf("Timeout waiting for semaphore %s after %d seconds", prevFrameSemName, maxWaitSeconds),
			}, nil
		}
		
		// Wait a bit before retrying (like your Python 10ms sleep)
		time.Sleep(10 * time.Millisecond)
	}

	// Acquire the semaphore (this will block until data is available)
	if err := semPrev.Wait(); err != nil {
		semPrev.Close()
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to wait on semaphore: %v", err),
		}, nil
	}

	// Read raw data from shared memory for the previous frame
	data, err := h.readSharedMemory(req.StateID, req.VDAGID, req.FrameID-1)
	if err != nil {
		semPrev.Close()
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read shared memory: %v", err),
		}, nil
	}

	// Clean up previous semaphore after successful read (READ responsibility #3)
	if err := semaphore.Unlink(prevFrameSemName); err != nil {
		log.Printf("[StateProxy] Warning: Failed to unlink previous semaphore %s: %v", prevFrameSemName, err)
	}
	semPrev.Close()

	// DO NOT release current frame's semaphore here - that's the WRITE operation's responsibility
	// The current frame's semaphore will be released by handlePutState when data is written
	// Also, don't close semCurrent here - let the PUT operation handle it

	log.Printf("[StateProxy] Successfully retrieved state for %s", req.StateID)
	
	return StateResponse{
		Success: true,
		Data:    data, // Raw data from shared memory
	}, nil
}

// handlePutState handles state storage with proper semaphore coordination
// WRITE Operation responsibilities:
// 1. Grabs semaphore created by current request's read
// 2. Writes data to shared memory
// 3. Increments/releases semaphore to signal data is ready for next frame
// 4. Creates state if it doesn't exist (first request)
func (h *StateProxyHandler) handlePutState(req *StateRequest) (StateResponse, error) {
	log.Printf("[StateProxy] Putting state for state_id=%s, vdag_id=%s, frame_id=%d", 
		req.StateID, req.VDAGID, req.FrameID)

	// Construct semaphore name for the current frame
	semName := fmt.Sprintf("/%s_%s_%d_sem", req.StateID, req.VDAGID, req.FrameID)
	
	// Grab semaphore created by current request's read (WRITE responsibility #1)
	// Try to open existing semaphore first
	sem, err := semaphore.Open(semName, semaphore.O_RDWR, 0666, 0)
	if err != nil {
		// If semaphore doesn't exist, try to create it (for frame 0 case)
		if strings.Contains(err.Error(), "No such file or directory") {
			sem, err = semaphore.Open(semName, semaphore.O_CREAT|semaphore.O_RDWR, 0666, 0)
			if err != nil {
				return StateResponse{
					Success: false,
					Error:   fmt.Sprintf("Failed to create semaphore for write operation: %v", err),
				}, nil
			}
		} else {
			return StateResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to grab semaphore created by read operation: %v", err),
			}, nil
		}
	}
	defer sem.Close()

	// Write raw data to shared memory (creates state if it doesn't exist - WRITE responsibility #3)
	if err := h.writeSharedMemory(req.StateID, req.VDAGID, req.FrameID, req.Data); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to write shared memory: %v", err),
		}, nil
	}
	
	// Increment/release semaphore to signal data is ready (WRITE responsibility #2)
	if err := sem.Post(); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to post semaphore: %v", err),
		}, nil
	}

	// Close the semaphore after we're done with it
	sem.Close()

	log.Printf("[StateProxy] Successfully stored state for %s", req.StateID)
	
	return StateResponse{
		Success: true,
		Data:    []byte{}, // Empty for Put operations
	}, nil
}

// sendErrorResponse sends an error response
func (h *StateProxyHandler) sendErrorResponse(w http.ResponseWriter, message string, err error) {
	log.Printf("[StateProxy] Error: %s - %v", message, err)
	
	response := StateResponse{
		Success: false,
		Error:   fmt.Sprintf("%s: %v", message, err),
	}
	
	responseJSON, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // Return HTTP 200 with error in JSON body
	w.Write(responseJSON)
} 