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
    parser.add_argument('--mode', choices=['sequential', 'parallel', 'both'], default='both',
                      help='Test mode: sequential, parallel, or both')
    parser.add_argument('--core', type=int, default=4,
                      help='Core number for sequential test (default: 4)')
    parser.add_argument('--parallel', type=int, default=4,
                      help='Number of parallel processes (default: 4)')
    parser.add_argument('--iterations', type=int, default=100,
                      help='Number of iterations per test (default: 100)')
    
    args = parser.parse_args()
    
    if args.mode in ['sequential', 'both']:
        seq_time, seq_freq = run_sequential_test(args.image_path, args.core, args.iterations)
    
    if args.mode in ['parallel', 'both']:
        par_times, par_freqs = run_parallel_test(args.image_path, args.parallel, args.iterations)
        
        if args.mode == 'both':
            avg_par_time = sum(par_times) / len(par_times)
            print(f"\nPerformance comparison:")
            print(f"Sequential (core {args.core}): {seq_time:.2f}ms")
            print(f"Parallel (average): {avg_par_time:.2f}ms")
            print(f"Speedup: {seq_time/avg_par_time:.2f}x")
            if seq_freq and par_freqs:
                avg_par_freq = sum(par_freqs) / len(par_freqs)
                print(f"\nFrequency comparison:")
                print(f"Sequential frequency: {seq_freq:.2f}MHz")
                print(f"Parallel average frequency: {avg_par_freq:.2f}MHz")
                print(f"Frequency difference: {abs(seq_freq - avg_par_freq):.2f}MHz")

if __name__ == "__main__":
    main()