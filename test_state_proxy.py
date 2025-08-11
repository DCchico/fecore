#!/usr/bin/env python3
"""
State Proxy Testing Script

This script tests the Go State Proxy implementation to ensure:
1. Basic accessibility and functionality
2. Proper semaphore-based ordering guarantees
3. Complete workflow with frame sequence
4. Error handling and edge cases
"""

import requests
import json
import time
import base64
import threading
import random
import sys
import subprocess
import os
from typing import Dict, Any, Optional

class StateProxyTester:
    def __init__(self, base_url: str = "http://localhost:8081"):
        self.base_url = base_url
        self.state_endpoint = f"{base_url}/state"
        self.test_results = []
        
    def log(self, message: str, level: str = "INFO"):
        """Log messages with timestamp"""
        timestamp = time.strftime("%Y-%m-%d %H:%M:%S")
        print(f"[{timestamp}] [{level}] {message}")
        
    def cleanup_shared_memory(self):
        """Clean up any existing shared memory and semaphores"""
        self.log("Cleaning up existing shared memory and semaphores...")
        try:
            # Remove test-related semaphores and shared memory
            subprocess.run(["sudo", "rm", "-f", "/dev/shm/vt_*", "/dev/shm/test_*"], 
                         capture_output=True, text=True)
            self.log("✓ Cleanup completed")
        except Exception as e:
            self.log(f"Warning: Cleanup failed: {e}", "WARNING")
        
    def test_basic_accessibility(self) -> bool:
        """Test 1: Basic accessibility - can we reach the state proxy?"""
        self.log("=== Test 1: Basic Accessibility ===")
        
        try:
            # Test GET request with minimal parameters
            params = {
                "state_id": "test",
                "vdag_id": "test_vdag",
                "frame_id": "0",
                "op": "Get"
            }
            
            response = requests.get(self.state_endpoint, params=params, timeout=5)
            self.log(f"GET request status: {response.status_code}")
            
            if response.status_code == 200:
                result = response.json()
                self.log(f"GET response: {result}")
                self.test_results.append(("Basic Accessibility", True, "State proxy is accessible"))
                return True
            else:
                self.log(f"GET request failed: {response.text}", "ERROR")
                self.test_results.append(("Basic Accessibility", False, f"HTTP {response.status_code}"))
                return False
                
        except requests.exceptions.RequestException as e:
            self.log(f"Connection error: {e}", "ERROR")
            self.test_results.append(("Basic Accessibility", False, str(e)))
            return False
    
    def test_frame_0_workflow(self) -> bool:
        """Test 2: Frame 0 workflow - should get nothing but create semaphore"""
        self.log("=== Test 2: Frame 0 Workflow ===")
        
        try:
            # Frame 0 GET - should return no data but create semaphore
            params = {
                "state_id": "vt",
                "vdag_id": "vDAG1",
                "frame_id": "0",
                "op": "Get"
            }
            
            response = requests.get(self.state_endpoint, params=params, timeout=10)
            self.log(f"Frame 0 GET status: {response.status_code}")
            
            if response.status_code == 200:
                result = response.json()
                self.log(f"Frame 0 GET response: {result}")
                
                # Should return success: false for frame 0
                if not result.get("success", True):
                    self.log("✓ Frame 0 GET correctly returned no data")
                    
                    # Now test Frame 0 PUT - should store data and grab semaphore
                    put_data = {
                        "state_id": "vt",
                        "vdag_id": "vDAG1", 
                        "frame_id": 0,
                        "op": "Put",
                        "data": base64.b64encode(b"frame_0_data").decode('utf-8')
                    }
                    
                    put_response = requests.post(self.state_endpoint, json=put_data, timeout=10)
                    self.log(f"Frame 0 PUT status: {put_response.status_code}")
                    
                    if put_response.status_code == 200:
                        put_result = put_response.json()
                        self.log(f"Frame 0 PUT response: {put_result}")
                        
                        if put_result.get("success", False):
                            self.log("✓ Frame 0 PUT successfully stored data")
                            self.test_results.append(("Frame 0 Workflow", True, "Frame 0 workflow completed successfully"))
                            return True
                        else:
                            self.log(f"Frame 0 PUT failed: {put_result.get('error')}", "ERROR")
                            self.test_results.append(("Frame 0 Workflow", False, f"PUT failed: {put_result.get('error')}"))
                            return False
                    else:
                        self.log(f"Frame 0 PUT HTTP error: {put_response.text}", "ERROR")
                        self.test_results.append(("Frame 0 Workflow", False, f"PUT HTTP {put_response.status_code}"))
                        return False
                else:
                    self.log("Frame 0 GET should return success: false", "ERROR")
                    self.test_results.append(("Frame 0 Workflow", False, "Frame 0 GET should return success: false"))
                    return False
            else:
                self.log(f"Frame 0 GET HTTP error: {response.text}", "ERROR")
                self.test_results.append(("Frame 0 Workflow", False, f"GET HTTP {response.status_code}"))
                return False
                
        except Exception as e:
            self.log(f"Frame 0 workflow error: {e}", "ERROR")
            self.test_results.append(("Frame 0 Workflow", False, str(e)))
            return False
    
    def test_frame_sequence_ordering(self) -> bool:
        """Test 3: Complete frame sequence with ordering guarantees"""
        self.log("=== Test 3: Frame Sequence Ordering ===")
        
        try:
            # Step 1: Frame 0 PUT (store initial data)
            frame_0_data = {
                "state_id": "vt",
                "vdag_id": "vDAG1",
                "frame_id": 0,
                "op": "Put", 
                "data": base64.b64encode(b"initial_data_from_frame_0").decode('utf-8')
            }
            
            response = requests.post(self.state_endpoint, json=frame_0_data, timeout=10)
            if response.status_code != 200:
                self.log(f"Frame 0 PUT failed: {response.text}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 0 PUT failed"))
                return False
                
            result = response.json()
            if not result.get("success", False):
                self.log(f"Frame 0 PUT failed: {result.get('error')}", "ERROR")
                self.test_results.append(("Frame Sequence", False, f"Frame 0 PUT failed: {result.get('error')}"))
                return False
                
            self.log("✓ Frame 0 PUT completed")
            
            # Step 2: Frame 1 GET (should get frame 0 data)
            params = {
                "state_id": "vt",
                "vdag_id": "vDAG1",
                "frame_id": "1",
                "op": "Get"
            }
            
            response = requests.get(self.state_endpoint, params=params, timeout=15)
            if response.status_code != 200:
                self.log(f"Frame 1 GET failed: {response.text}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 1 GET failed"))
                return False
                
            result = response.json()
            if not result.get("success", False):
                self.log(f"Frame 1 GET failed: {result.get('error')}", "ERROR")
                self.test_results.append(("Frame Sequence", False, f"Frame 1 GET failed: {result.get('error')}"))
                return False
                
            # Verify we got the correct data
            data = base64.b64decode(result.get("data", ""))
            if data == b"initial_data_from_frame_0":
                self.log("✓ Frame 1 GET retrieved correct frame 0 data")
            else:
                self.log(f"Frame 1 GET got wrong data: {data}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 1 GET got wrong data"))
                return False
            
            # Step 3: Frame 1 PUT (store enhanced data)
            enhanced_data = b"initial_data_from_frame_0" + b"_enhanced_by_frame_1"
            frame_1_data = {
                "state_id": "vt",
                "vdag_id": "vDAG1",
                "frame_id": 1,
                "op": "Put",
                "data": base64.b64encode(enhanced_data).decode('utf-8')
            }
            
            response = requests.post(self.state_endpoint, json=frame_1_data, timeout=10)
            if response.status_code != 200:
                self.log(f"Frame 1 PUT failed: {response.text}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 1 PUT failed"))
                return False
                
            result = response.json()
            if not result.get("success", False):
                self.log(f"Frame 1 PUT failed: {result.get('error')}", "ERROR")
                self.test_results.append(("Frame Sequence", False, f"Frame 1 PUT failed: {result.get('error')}"))
                return False
                
            self.log("✓ Frame 1 PUT completed")
            
            # Step 4: Frame 2 GET (should get frame 1 data)
            params = {
                "state_id": "vt",
                "vdag_id": "vDAG1", 
                "frame_id": "2",
                "op": "Get"
            }
            
            response = requests.get(self.state_endpoint, params=params, timeout=15)
            if response.status_code != 200:
                self.log(f"Frame 2 GET failed: {response.text}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 2 GET failed"))
                return False
                
            result = response.json()
            if not result.get("success", False):
                self.log(f"Frame 2 GET failed: {result.get('error')}", "ERROR")
                self.test_results.append(("Frame Sequence", False, f"Frame 2 GET failed: {result.get('error')}"))
                return False
                
            # Verify we got the enhanced data
            data = base64.b64decode(result.get("data", ""))
            if data == enhanced_data:
                self.log("✓ Frame 2 GET retrieved correct frame 1 data")
            else:
                self.log(f"Frame 2 GET got wrong data: {data}", "ERROR")
                self.test_results.append(("Frame Sequence", False, "Frame 2 GET got wrong data"))
                return False
            
            self.log("✓ Complete frame sequence test passed")
            self.test_results.append(("Frame Sequence", True, "Complete frame sequence with ordering worked correctly"))
            return True
            
        except Exception as e:
            self.log(f"Frame sequence error: {e}", "ERROR")
            self.test_results.append(("Frame Sequence", False, str(e)))
            return False
    
    def test_concurrent_access(self) -> bool:
        """Test 4: Concurrent access - multiple threads accessing same frame"""
        self.log("=== Test 4: Concurrent Access ===")
        
        results = []
        errors = []
        
        def worker(thread_id: int, frame_id: int):
            try:
                # Worker tries to GET frame data
                params = {
                    "state_id": "vt",
                    "vdag_id": "vDAG1",
                    "frame_id": str(frame_id),
                    "op": "Get"
                }
                
                response = requests.get(self.state_endpoint, params=params, timeout=10)
                if response.status_code == 200:
                    result = response.json()
                    if result.get("success", False):
                        data = base64.b64decode(result.get("data", ""))
                        results.append((thread_id, frame_id, data))
                    else:
                        errors.append((thread_id, frame_id, result.get("error")))
                else:
                    errors.append((thread_id, frame_id, f"HTTP {response.status_code}"))
                    
            except Exception as e:
                errors.append((thread_id, frame_id, str(e)))
        
        # First, set up frame 0 data
        frame_0_data = {
            "state_id": "vt",
            "vdag_id": "vDAG1",
            "frame_id": 0,
            "op": "Put",
            "data": base64.b64encode(b"concurrent_test_data").decode('utf-8')
        }
        
        response = requests.post(self.state_endpoint, json=frame_0_data, timeout=10)
        if response.status_code != 200 or not response.json().get("success", False):
            self.log("Failed to set up concurrent test data", "ERROR")
            self.test_results.append(("Concurrent Access", False, "Failed to set up test data"))
            return False
        
        # Start multiple threads trying to access frame 1
        threads = []
        for i in range(5):
            thread = threading.Thread(target=worker, args=(i, 1))
            threads.append(thread)
            thread.start()
        
        # Wait for all threads to complete
        for thread in threads:
            thread.join()
        
        # Analyze results - with new coordination pattern, only one should succeed
        if len(results) == 1 and len(errors) == 4:
            # Only one thread should succeed, others should fail
            successful_thread, frame_id, data = results[0]
            self.log(f"✓ Concurrent access test passed: thread {successful_thread} succeeded, others failed as expected")
            self.log(f"  Successful data: {data}")
            
            # Check that other errors are expected (semaphore coordination failures)
            coordination_errors = [e for e in errors if any(keyword in str(e[2]).lower() 
                                                         for keyword in ["semaphore", "timeout", "failed"])]
            if len(coordination_errors) >= 3:  # At least 3 should be coordination errors
                self.log("✓ All other threads correctly got coordination errors")
                self.test_results.append(("Concurrent Access", True, "Concurrent access properly serialized"))
                return True
            else:
                self.log(f"Unexpected errors: {errors}", "ERROR")
                self.test_results.append(("Concurrent Access", False, "Unexpected error types"))
                return False
        else:
            self.log(f"Unexpected results: {results}, errors: {errors}", "ERROR")
            self.test_results.append(("Concurrent Access", False, f"Unexpected results: {len(results)} success, {len(errors)} errors"))
            return False
    
    def test_error_handling(self) -> bool:
        """Test 5: Error handling - invalid requests, missing data, etc."""
        self.log("=== Test 5: Error Handling ===")
        
        test_cases = [
            # Missing state_id
            {
                "params": {"vdag_id": "vDAG1", "frame_id": "0", "op": "Get"},
                "expected_error": "state_id is required"
            },
            # Missing vdag_id  
            {
                "params": {"state_id": "vt", "frame_id": "0", "op": "Get"},
                "expected_error": "vdag_id is required"
            },
            # Invalid frame_id
            {
                "params": {"state_id": "vt", "vdag_id": "vDAG1", "frame_id": "-1", "op": "Get"},
                "expected_error": "frame_id must be non-negative"
            },
            # Invalid operation
            {
                "params": {"state_id": "vt", "vdag_id": "vDAG1", "frame_id": "0", "op": "Invalid"},
                "expected_error": "op must be 'Get' or 'Put'"
            },
            # Try to get frame 1 data when frame 0 doesn't exist
            {
                "params": {"state_id": "vt", "vdag_id": "vDAG1", "frame_id": "1", "op": "Get"},
                "expected_error": "Timeout waiting for semaphore"
            }
        ]
        
        all_passed = True
        
        for i, test_case in enumerate(test_cases):
            try:
                response = requests.get(self.state_endpoint, params=test_case["params"], timeout=10)
                
                if response.status_code == 200:
                    result = response.json()
                    if not result.get("success", True) and test_case["expected_error"] in result.get("error", ""):
                        self.log(f"✓ Test case {i+1} passed: got expected error")
                    else:
                        self.log(f"✗ Test case {i+1} failed: unexpected response {result}", "ERROR")
                        all_passed = False
                else:
                    self.log(f"✗ Test case {i+1} failed: HTTP {response.status_code}", "ERROR")
                    all_passed = False
                    
            except Exception as e:
                self.log(f"✗ Test case {i+1} failed with exception: {e}", "ERROR")
                all_passed = False
        
        if all_passed:
            self.log("✓ All error handling tests passed")
            self.test_results.append(("Error Handling", True, "All error cases handled correctly"))
            return True
        else:
            self.test_results.append(("Error Handling", False, "Some error cases not handled correctly"))
            return False
    
    def run_all_tests(self) -> Dict[str, Any]:
        """Run all tests and return summary"""
        self.log("Starting State Proxy Tests")
        self.log(f"Testing endpoint: {self.state_endpoint}")
        
        # Clean up before running tests
        self.cleanup_shared_memory()
        
        tests = [
            ("Basic Accessibility", self.test_basic_accessibility),
            ("Frame 0 Workflow", self.test_frame_0_workflow),
            ("Frame Sequence Ordering", self.test_frame_sequence_ordering),
            ("Concurrent Access", self.test_concurrent_access),
            ("Error Handling", self.test_error_handling)
        ]
        
        results = {}
        passed = 0
        total = len(tests)
        
        for test_name, test_func in tests:
            self.log(f"\n{'='*50}")
            self.log(f"Running: {test_name}")
            self.log(f"{'='*50}")
            
            try:
                success = test_func()
                results[test_name] = success
                if success:
                    passed += 1
                    
                # Add delay between tests
                time.sleep(1)
                
            except Exception as e:
                self.log(f"Test {test_name} crashed: {e}", "ERROR")
                results[test_name] = False
        
        # Clean up after running tests
        self.cleanup_shared_memory()
        
        # Print summary
        self.log(f"\n{'='*60}")
        self.log("TEST SUMMARY")
        self.log(f"{'='*60}")
        
        for test_name, success in results.items():
            status = "PASS" if success else "FAIL"
            self.log(f"{test_name}: {status}")
        
        self.log(f"\nOverall: {passed}/{total} tests passed")
        
        # Print detailed results
        self.log(f"\n{'='*60}")
        self.log("DETAILED RESULTS")
        self.log(f"{'='*60}")
        
        for test_name, success, details in self.test_results:
            status = "PASS" if success else "FAIL"
            self.log(f"{test_name}: {status} - {details}")
        
        return {
            "total_tests": total,
            "passed": passed,
            "failed": total - passed,
            "results": results,
            "detailed_results": self.test_results
        }

def main():
    """Main function to run the tests"""
    import argparse
    
    parser = argparse.ArgumentParser(description="Test State Proxy functionality")
    parser.add_argument("--url", default="http://localhost:8081", 
                       help="Base URL of the state proxy (default: http://localhost:8081)")
    parser.add_argument("--test", choices=["all", "basic", "frame0", "sequence", "concurrent", "errors"],
                       default="all", help="Specific test to run (default: all)")
    
    args = parser.parse_args()
    
    tester = StateProxyTester(args.url)
    
    if args.test == "all":
        summary = tester.run_all_tests()
        sys.exit(0 if summary["failed"] == 0 else 1)
    else:
        # Run specific test
        test_map = {
            "basic": tester.test_basic_accessibility,
            "frame0": tester.test_frame_0_workflow,
            "sequence": tester.test_frame_sequence_ordering,
            "concurrent": tester.test_concurrent_access,
            "errors": tester.test_error_handling
        }
        
        test_func = test_map.get(args.test)
        if test_func:
            # Clean up before running specific test
            tester.cleanup_shared_memory()
            success = test_func()
            # Clean up after running specific test
            tester.cleanup_shared_memory()
            sys.exit(0 if success else 1)
        else:
            print(f"Unknown test: {args.test}")
            sys.exit(1)

if __name__ == "__main__":
    main() 