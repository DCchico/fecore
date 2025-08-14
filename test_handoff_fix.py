#!/usr/bin/env python3
"""
Simple test to verify state proxy handoff fix
"""

import requests
import json
import time
import base64
import subprocess
import os
import sys
from multiprocessing import shared_memory
import struct
import posix_ipc

def cleanup():
    """Clean up shared memory and semaphores"""
    print("Cleaning up...")
    try:
        subprocess.run(["sudo", "rm", "-f", "/dev/shm/vt_*"], capture_output=True, text=True)
        print("✓ Cleanup completed")
    except Exception as e:
        print(f"Warning: Cleanup failed: {e}")

def test_handoff():
    """Test state proxy to Python handoff"""
    print("=== Testing State Proxy to Python Handoff ===")
    
    base_url = "http://localhost:8081"
    camera_id = "vDAG1"
    test_data = b"test_data"
    
    try:
        # Step 1: State proxy manages frames 1-3
        print("Step 1: State proxy managing frames 1-3...")
        
        for frame_id in range(1, 4):
            # PUT frame data via state proxy
            put_data = {
                "state_id": "vt",
                "vdag_id": camera_id,
                "frame_id": frame_id,
                "op": "Put",
                "data": base64.b64encode(test_data + f"_frame_{frame_id}".encode()).decode('utf-8')
            }
            
            response = requests.post(f"{base_url}/state", json=put_data, timeout=10)
            if response.status_code != 200:
                print(f"Frame {frame_id} PUT failed: {response.text}")
                return False
                
            result = response.json()
            if not result.get("success", False):
                print(f"Frame {frame_id} PUT failed: {result.get('error')}")
                return False
                
            print(f"✓ Frame {frame_id} PUT via state proxy successful")
        
        # Step 2: Python function takes over frame 4
        print("Step 2: Python function taking over frame 4...")
        
        # Check if shared memory exists
        shm_name = f"vt_{camera_id}"
        try:
            shm = shared_memory.SharedMemory(name=shm_name, create=False)
            print("✓ Shared memory exists and accessible")
        except FileNotFoundError:
            print("✗ Shared memory not found")
            return False
        
        # Try to open previous frame's semaphore (frame 3)
        sem_prev_name = f"/vt_{camera_id}_3_sem"
        try:
            sem_prev = posix_ipc.Semaphore(sem_prev_name)
            print("✓ Previous frame semaphore exists")
            
            # Try to acquire the semaphore
            sem_prev.acquire(timeout=1)  # Should succeed immediately
            print("✓ Successfully acquired previous frame semaphore")
            
            # Read data from shared memory
            header = bytes(shm.buf[:24])
            current_frame, state_size, valid = struct.unpack("!QQQ", header)
            
            if valid == 1 and current_frame == 3:
                data = bytes(shm.buf[24:24+state_size])
                print(f"✓ Successfully read frame 3 data: {data[:50]}...")
            else:
                print(f"✗ Invalid shared memory state: frame={current_frame}, valid={valid}")
                return False
            
            # Clean up previous semaphore
            sem_prev.unlink()
            sem_prev.close()
            
        except posix_ipc.ExistentialError:
            print("✗ Previous frame semaphore not found")
            return False
        except Exception as e:
            print(f"✗ Error accessing previous frame semaphore: {e}")
            return False
        
        # Create current frame's semaphore
        sem_current_name = f"/vt_{camera_id}_4_sem"
        try:
            sem_current = posix_ipc.Semaphore(sem_current_name, posix_ipc.O_CREAT | posix_ipc.O_EXCL, initial_value=0)
            print("✓ Created current frame semaphore")
        except posix_ipc.ExistentialError:
            print("✗ Current frame semaphore already exists (concurrent access)")
            return False
        
        # Write data to shared memory
        enhanced_data = test_data + b"_frame_4_enhanced_by_python"
        
        if len(enhanced_data) > len(shm.buf) - 24:
            print("✗ Data too large for shared memory")
            return False
        
        # Write data first, then header
        shm.buf[24:24+len(enhanced_data)] = enhanced_data
        shm.buf[:24] = struct.pack("!QQQ", 4, len(enhanced_data), 1)
        
        # Release semaphore to signal completion
        sem_current.release()
        print("✓ Successfully stored frame 4 data and released semaphore")
        
        # Clean up
        sem_current.close()
        shm.close()
        
        print("✓ State proxy to Python handoff test passed")
        return True
        
    except Exception as e:
        print(f"Handoff test error: {e}")
        return False

def main():
    """Main function"""
    print("Starting State Proxy Handoff Fix Test")
    print("Make sure state proxy is running on http://localhost:8081")
    
    # Clean up before test
    cleanup()
    
    # Run test
    success = test_handoff()
    
    # Clean up after test
    cleanup()
    
    if success:
        print("\n🎉 Handoff test passed! State proxy and Python function coordinate correctly.")
        sys.exit(0)
    else:
        print("\n❌ Handoff test failed. Coordination issues remain.")
        sys.exit(1)

if __name__ == "__main__":
    main() 