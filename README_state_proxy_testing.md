# State Proxy Testing Suite

This testing suite validates the Go State Proxy implementation to ensure it correctly handles state management and ordering guarantees for function invocations.

## Overview

The State Proxy is designed to:
1. **Guarantee ordering** of function invocations using POSIX semaphores
2. **Manage shared state** between function frames using shared memory
3. **Handle concurrent access** by serializing requests to the same frame
4. **Provide error handling** for various edge cases

## Test Scenarios

### 1. Basic Accessibility Test
- **Purpose**: Verify the state proxy is reachable and responds to basic requests
- **What it tests**: HTTP connectivity, basic GET/PUT operations
- **Expected**: State proxy should be accessible and return proper JSON responses

### 2. Frame 0 Workflow Test
- **Purpose**: Test the special case of frame 0 (first frame)
- **What it tests**: 
  - Frame 0 GET should return `success: false` (no previous data)
  - Frame 0 should still create a semaphore for subsequent frames
  - Frame 0 PUT should store data and create semaphore
- **Expected**: Frame 0 creates the foundation for the frame sequence

### 3. Frame Sequence Ordering Test
- **Purpose**: Test the complete workflow with ordering guarantees
- **What it tests**:
  ```
  Frame 0 PUT → Frame 1 GET → Frame 1 PUT → Frame 2 GET
  ```
- **Expected**: Each frame should get the data from the previous frame and add to it

### 4. Concurrent Access Test
- **Purpose**: Verify that concurrent access to the same frame is properly serialized
- **What it tests**: Multiple threads trying to access the same frame simultaneously
- **Expected**: Only one thread should succeed, others should get "concurrent_read_attempt" error

### 5. Error Handling Test
- **Purpose**: Verify proper error handling for invalid requests
- **What it tests**: Missing parameters, invalid values, timeout scenarios
- **Expected**: Appropriate error messages for each error case

## Usage

### Prerequisites

1. **Python 3** with `requests` module:
   ```bash
   pip3 install requests
   ```

2. **State Proxy running** on the target URL (default: `http://localhost:8081`)

### Running Tests

#### Option 1: Using the shell script (recommended)
```bash
# Run all tests
./run_state_proxy_tests.sh

# Test remote state proxy
./run_state_proxy_tests.sh -u http://192.168.1.100:8081

# Run specific test
./run_state_proxy_tests.sh -t sequence

# Show help
./run_state_proxy_tests.sh -h
```

#### Option 2: Using Python directly
```bash
# Run all tests
python3 test_state_proxy.py

# Test remote state proxy
python3 test_state_proxy.py --url http://192.168.1.100:8081

# Run specific test
python3 test_state_proxy.py --test sequence

# Show help
python3 test_state_proxy.py --help
```

### Test Types

- `all` - Run all tests (default)
- `basic` - Basic accessibility test only
- `frame0` - Frame 0 workflow test only
- `sequence` - Frame sequence ordering test only
- `concurrent` - Concurrent access test only
- `errors` - Error handling test only

## Expected Workflow

The state proxy implements the following workflow:

### Frame 0 (First Frame)
1. **GET request**: Returns `success: false` (no previous data exists)
2. **Creates semaphore**: `/vt_vDAG1_0_sem` to signal frame 0 completion
3. **PUT request**: Stores data and releases semaphore

### Frame N (N > 0)
1. **GET request**: 
   - Waits for previous frame's semaphore (`/vt_vDAG1_{N-1}_sem`)
   - Reads data from shared memory
   - Creates current frame's semaphore (`/vt_vDAG1_N_sem`)
2. **PUT request**: 
   - Stores enhanced data
   - Releases current frame's semaphore

## Shared Memory Format

The state proxy uses POSIX shared memory with the following format:

```
Header (24 bytes):
- frame_id (8 bytes, big-endian)
- state_size (8 bytes, big-endian) 
- valid_flag (8 bytes, big-endian, 1 = valid)

Data (variable size):
- Raw serialized data
```

## Semaphore Naming Convention

Semaphores follow the pattern: `/state_id_vdag_id_frame_id_sem`

Examples:
- `/vt_vDAG1_0_sem` - Frame 0 semaphore
- `/vt_vDAG1_1_sem` - Frame 1 semaphore
- `/vt_vDAG1_2_sem` - Frame 2 semaphore

## Troubleshooting

### Common Issues

1. **Connection refused**: State proxy not running or wrong URL
2. **Timeout errors**: Function containers not ready (cold start issue)
3. **Permission denied**: Shared memory or semaphore permissions
4. **Concurrent access errors**: Expected behavior for multiple threads

### Debug Commands

```bash
# Check if state proxy is running
curl http://localhost:8081/state

# Check shared memory
ls -la /dev/shm/ | grep vt

# Check semaphores
ls -la /dev/shm/ | grep sem

# Check state proxy logs
journalctl -u fecore-provider -f
```

### Expected Test Output

Successful test run should show:
```
==========================================
    State Proxy Testing Suite
==========================================

[INFO] Checking dependencies...
[SUCCESS] Dependencies check passed
[INFO] Checking state proxy accessibility...
[SUCCESS] State proxy is accessible at http://localhost:8081
[INFO] Starting state proxy tests...

==================================================
Running: Basic Accessibility
==================================================
[INFO] === Test 1: Basic Accessibility ===
[INFO] GET request status: 200
[INFO] GET response: {'success': False, 'error': 'No previous state exists for frame 0'}

==================================================
Running: Frame 0 Workflow
==================================================
[INFO] === Test 2: Frame 0 Workflow ===
[INFO] Frame 0 GET status: 200
[INFO] Frame 0 GET response: {'success': False, 'error': 'No previous state exists for frame 0'}
[INFO] ✓ Frame 0 GET correctly returned no data
[INFO] Frame 0 PUT status: 200
[INFO] Frame 0 PUT response: {'success': True, 'data': b''}
[INFO] ✓ Frame 0 PUT successfully stored data

...

[SUCCESS] All tests passed!
```

## Integration with Function Code

The state proxy is designed to work with your Python function code. The function should:

1. **Parse remote info** from `dependent-state-location` header
2. **Make GET requests** to retrieve previous frame's state
3. **Make PUT requests** to store current frame's state
4. **Handle errors** gracefully (timeouts, missing data, etc.)

Example integration:
```python
# In your Python function
if dependent_state_mode.startswith("remote:"):
    host, port = parse_remote_info(dependent_state_mode)
    
    # GET previous state
    response = requests.get(f"http://{host}:{port}/state", params={
        "state_id": "vt",
        "vdag_id": camera_id,
        "frame_id": frame_id - 1,
        "op": "Get"
    })
    
    # PUT current state
    response = requests.post(f"http://{host}:{port}/state", json={
        "state_id": "vt",
        "vdag_id": camera_id,
        "frame_id": frame_id,
        "op": "Put",
        "data": base64.b64encode(serialized_data).decode('utf-8')
    })
``` 