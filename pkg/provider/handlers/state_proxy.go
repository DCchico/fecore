package handlers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
		req.Op = "Get"
		
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
		h.sendErrorResponse(w, "Invalid request", err)
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

// handleGetState handles state retrieval with proper semaphore waiting logic
func (h *StateProxyHandler) handleGetState(req *StateRequest) (StateResponse, error) {
	log.Printf("[StateProxy] Getting state for state_id=%s, vdag_id=%s, frame_id=%d", 
		req.StateID, req.VDAGID, req.FrameID)

	// Construct semaphore name based on your Python naming convention
	semName := fmt.Sprintf("/%s_%s_%d_sem", req.StateID, req.VDAGID, req.FrameID)
	
	// Wait for semaphore to exist and be ready (similar to your Python logic)
	var sem *semaphore.Semaphore
	var err error
	maxWaitSeconds := 10
	waitStart := time.Now()
	
	for {
		// Try to open existing semaphore
		sem, err = semaphore.Open(semName, semaphore.O_RDWR, 0666, 0)
		if err == nil {
			break // Semaphore exists, proceed
		}
		
		// Check if we've timed out
		if time.Since(waitStart) >= time.Duration(maxWaitSeconds)*time.Second {
			return StateResponse{
				Success: false, 
				Error:   fmt.Sprintf("Timeout waiting for semaphore after %d seconds", maxWaitSeconds),
			}, nil
		}
		
		// Wait a bit before retrying (like your Python 10ms sleep)
		time.Sleep(10 * time.Millisecond)
	}
	defer sem.Close()

	// Try to acquire the semaphore with remaining time
	remainingTime := time.Duration(maxWaitSeconds)*time.Second - time.Since(waitStart)
	if remainingTime <= 0 {
		return StateResponse{
			Success: false,
			Error:   "No time remaining to acquire semaphore",
		}, nil
	}
	
	// Acquire the semaphore (this will block until data is available)
	if err := sem.Wait(); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to wait on semaphore: %v", err),
		}, nil
	}

	// Release the semaphore immediately after acquiring
	if err := sem.Post(); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to release semaphore: %v", err),
		}, nil
	}

	// Read raw data from shared memory (blind forwarder)
	data, err := h.readSharedMemory(req.StateID, req.VDAGID, req.FrameID)
	if err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read shared memory: %v", err),
		}, nil
	}

	log.Printf("[StateProxy] Successfully retrieved state for %s", req.StateID)
	
	return StateResponse{
		Success: true,
		Data:    data, // Raw data from shared memory
	}, nil
}

// handlePutState handles state storage with proper semaphore creation
func (h *StateProxyHandler) handlePutState(req *StateRequest) (StateResponse, error) {
	log.Printf("[StateProxy] Putting state for state_id=%s, vdag_id=%s, frame_id=%d", 
		req.StateID, req.VDAGID, req.FrameID)

	// Construct semaphore name
	semName := fmt.Sprintf("/%s_%s_%d_sem", req.StateID, req.VDAGID, req.FrameID)
	
	// Create the semaphore (like your Python O_CREAT | O_EXCL)
	sem, err := semaphore.Open(semName, semaphore.O_CREAT|semaphore.O_EXCL, 0666, 0)
	if err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to create semaphore: %v", err),
		}, nil
	}
	defer sem.Close()

	// Write raw data to shared memory (blind forwarder)
	if err := h.writeSharedMemory(req.StateID, req.VDAGID, req.FrameID, req.Data); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to write shared memory: %v", err),
		}, nil
	}
	
	// Release the semaphore to indicate data is ready
	if err := sem.Post(); err != nil {
		return StateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to post semaphore: %v", err),
		}, nil
	}

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
	w.WriteHeader(http.StatusBadRequest)
	w.Write(responseJSON)
} 