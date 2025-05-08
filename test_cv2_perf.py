import cv2
import numpy as np
import time
import os
import psutil
import argparse
from concurrent.futures import ProcessPoolExecutor

def get_cpu_freq(core):
    try:
        with open(f'/sys/devices/system/cpu/cpu{core}/cpufreq/scaling_cur_freq', 'r') as f:
            return int(f.read().strip()) / 1000  # Convert to MHz
    except:
        return None

def set_cpu_affinity(core):
    p = psutil.Process()
    p.cpu_affinity([core])

def test_encode(image_path, core=None, num_iterations=100):
    # Read image
    img = cv2.imread(image_path)
    if img is None:
        raise ValueError(f"Could not read image: {image_path}")
    
    # Set CPU affinity if specified
    if core is not None:
        set_cpu_affinity(core)
    
    # Warm up
    for _ in range(5):
        cv2.imencode('.jpg', img)[1]
    
    # Measure encoding time and CPU frequency
    times = []
    freqs = []
    for _ in range(num_iterations):
        freq = get_cpu_freq(core) if core is not None else None
        start = time.perf_counter()
        cv2.imencode('.jpg', img)[1]
        end = time.perf_counter()
        times.append((end - start) * 1000)  # Convert to milliseconds
        if freq is not None:
            freqs.append(freq)
    
    return times, freqs

def run_sequential_test(image_path, core, num_iterations):
    print(f"\nRunning sequential test on core {core}")
    times, freqs = test_encode(image_path, core, num_iterations)
    avg_time = sum(times) / len(times)
    avg_freq = sum(freqs) / len(freqs) if freqs else None
    print(f"Average encoding time: {avg_time:.2f}ms")
    print(f"Min time: {min(times):.2f}ms")
    print(f"Max time: {max(times):.2f}ms")
    if avg_freq:
        print(f"Average CPU frequency: {avg_freq:.2f}MHz")
        print(f"Min frequency: {min(freqs):.2f}MHz")
        print(f"Max frequency: {max(freqs):.2f}MHz")
    return avg_time, avg_freq

def run_colocated_sequential_test(image_path, core, num_iterations):
    print(f"\nRunning colocated sequential tests on core {core}")
    start_time = time.time()
    
    with ProcessPoolExecutor(max_workers=2) as executor:
        futures = [executor.submit(test_encode, image_path, core, num_iterations) for _ in range(2)]
        results = [f.result() for f in futures]
    
    end_time = time.time()
    total_time = end_time - start_time
    
    print("\nResults for each process:")
    avg_times = []
    avg_freqs = []
    for i, (times, freqs) in enumerate(results):
        avg_time = sum(times) / len(times)
        avg_freq = sum(freqs) / len(freqs) if freqs else None
        avg_times.append(avg_time)
        if avg_freq:
            avg_freqs.append(avg_freq)
        print(f"\nProcess {i+1}:")
        print(f"Average encoding time: {avg_time:.2f}ms")
        print(f"Min time: {min(times):.2f}ms")
        print(f"Max time: {max(times):.2f}ms")
        if avg_freq:
            print(f"Average CPU frequency: {avg_freq:.2f}MHz")
            print(f"Min frequency: {min(freqs):.2f}MHz")
            print(f"Max frequency: {max(freqs):.2f}MHz")
    
    print(f"\nTotal execution time: {total_time:.2f} seconds")
    print(f"Average time per process: {sum(avg_times)/len(avg_times):.2f}ms")
    if avg_freqs:
        print(f"Average frequency across processes: {sum(avg_freqs)/len(avg_freqs):.2f}MHz")
    return avg_times, avg_freqs

def run_parallel_test(image_path, num_parallel, num_iterations):
    # Use cores 4-11 for testing
    cores = list(range(4, 4 + num_parallel))
    
    print(f"\nRunning {num_parallel} parallel encoding tests on cores {cores}")
    start_time = time.time()
    
    with ProcessPoolExecutor(max_workers=num_parallel) as executor:
        futures = [executor.submit(test_encode, image_path, core, num_iterations) for core in cores]
        results = [f.result() for f in futures]
    
    end_time = time.time()
    total_time = end_time - start_time
    
    print("\nResults for each core:")
    avg_times = []
    avg_freqs = []
    for core, (times, freqs) in zip(cores, results):
        avg_time = sum(times) / len(times)
        avg_freq = sum(freqs) / len(freqs) if freqs else None
        avg_times.append(avg_time)
        if avg_freq:
            avg_freqs.append(avg_freq)
        print(f"\nCore {core}:")
        print(f"Average encoding time: {avg_time:.2f}ms")
        print(f"Min time: {min(times):.2f}ms")
        print(f"Max time: {max(times):.2f}ms")
        if avg_freq:
            print(f"Average CPU frequency: {avg_freq:.2f}MHz")
            print(f"Min frequency: {min(freqs):.2f}MHz")
            print(f"Max frequency: {max(freqs):.2f}MHz")
    
    print(f"\nTotal execution time: {total_time:.2f} seconds")
    print(f"Average time per core: {sum(avg_times)/len(avg_times):.2f}ms")
    if avg_freqs:
        print(f"Average frequency across cores: {sum(avg_freqs)/len(avg_freqs):.2f}MHz")
    return avg_times, avg_freqs

def main():
    parser = argparse.ArgumentParser(description='Test CV2 encoding performance')
    parser.add_argument('image_path', help='Path to the image file')
    parser.add_argument('--mode', choices=['sequential', 'parallel', 'colocated', 'all'], default='all',
                      help='Test mode: sequential, parallel, colocated, or all')
    parser.add_argument('--core', type=int, default=4,
                      help='Core number for sequential/colocated tests (default: 4)')
    parser.add_argument('--parallel', type=int, default=4,
                      help='Number of parallel processes (default: 4)')
    parser.add_argument('--iterations', type=int, default=100,
                      help='Number of iterations per test (default: 100)')
    
    args = parser.parse_args()
    
    results = {}
    
    if args.mode in ['sequential', 'all']:
        seq_time, seq_freq = run_sequential_test(args.image_path, args.core, args.iterations)
        results['sequential'] = (seq_time, seq_freq)
    
    if args.mode in ['colocated', 'all']:
        col_times, col_freqs = run_colocated_sequential_test(args.image_path, args.core, args.iterations)
        results['colocated'] = (sum(col_times)/len(col_times), sum(col_freqs)/len(col_freqs) if col_freqs else None)
    
    if args.mode in ['parallel', 'all']:
        par_times, par_freqs = run_parallel_test(args.image_path, args.parallel, args.iterations)
        results['parallel'] = (sum(par_times)/len(par_times), sum(par_freqs)/len(par_freqs) if par_freqs else None)
    
    if len(results) > 1:
        print("\nPerformance comparison:")
        for mode, (time, freq) in results.items():
            print(f"{mode.capitalize()}: {time:.2f}ms")
            if freq:
                print(f"{mode.capitalize()} frequency: {freq:.2f}MHz")
        
        # Calculate speedups
        if 'sequential' in results and 'parallel' in results:
            seq_time = results['sequential'][0]
            par_time = results['parallel'][0]
            print(f"\nParallel vs Sequential speedup: {seq_time/par_time:.2f}x")
        
        if 'sequential' in results and 'colocated' in results:
            seq_time = results['sequential'][0]
            col_time = results['colocated'][0]
            print(f"Colocated vs Sequential speedup: {seq_time/col_time:.2f}x")
        
        if 'colocated' in results and 'parallel' in results:
            col_time = results['colocated'][0]
            par_time = results['parallel'][0]
            print(f"Parallel vs Colocated speedup: {col_time/par_time:.2f}x")

if __name__ == "__main__":
    main()