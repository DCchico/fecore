#/usr/bin/env python3

import subprocess
import sys
import threading
import time
import psutil

cpu_samples = []
mem_samples = []
preproc_timings = []
inf_timings = []
e2e_timings = []

# def do_preproc(input_image, output_tensor, shape, ctr_name, task_name):
#   START_TIME=time.time_ns()
#   result = subprocess.run(
#     ["/project/benchmarks/call_preproc_launcher.sh", input_image, output_tensor, shape],
#     capture_output=True, text=True
#   )
#   END_TIME=time.time_ns()
#   ELAPSED=int(END_TIME) - int(START_TIME)
#   return ELAPSED

def do_wasm_inf():
  START_TIME=time.time_ns()
  # result = subprocess.run(
  #     ["wasmedge",
  #     "--dir", ".:./data", "target/wasm32-wasi/debug/wasm-inference-aot.wasm"],
  #     capture_output=True, text=True
  # )
  result = subprocess.run(
      ["./wasminf.sh"],
      capture_output=True, text=True
  )
  print(result)
  END_TIME=time.time_ns()
  ELAPSED=int(END_TIME) - int(START_TIME)
  return ELAPSED

def do_native_inf_via_podman():
  START_TIME=time.time_ns()
  result = subprocess.run(
      ["./nativeinf.sh"],
      capture_output=True, text=True
  )
  END_TIME=time.time_ns()
  tmp = subprocess.run(
      ["podman", "rm", str(result), "&&", "podman", "kill", str(result)],
      capture_output=True, text=True
  )
  ELAPSED=int(END_TIME) - int(START_TIME)
  return ELAPSED

def do_wasm_inf_via_podman():
  START_TIME=time.time_ns()
  result = subprocess.run(
      ['podman', 'run', '--runtime=/usr/local/bin/crun',
      '-e', 'WASMEDGE_PLUGIN_PATH="/wasmedge/"', 
      '-e', 'LD_LIBRARY_PATH="/lib/:/wasmedge/"', 'wasinn-ex:latest'],
      capture_output=True, text=True
  )
  print(result)
  END_TIME=time.time_ns()
  ELAPSED=int(END_TIME) - int(START_TIME)
  return ELAPSED

def benchmark(TASK_NAME):
    global preproc_timings
    global inf_timings
    global e2e_timings
    START_TIME=time.time_ns()
    # inf_time = do_wasm_inf()
    inf_time = do_wasm_inf()
    # inf_time = do_wasm_inf_via_podman()
    END_TIME=time.time_ns()
    ELAPSED=int(END_TIME) - int(START_TIME)
    #print(f"inf,{inf_time}")
    print(f"{inf_time}")
    #print(f"e2e,{ELAPSED}")

# 5 RPS over 60 seconds
QPS_RANGE_START=1
#QPS_RANGE_END=301
QPS_RANGE_END=3
QPS_SLEEP=0.7

next_task = 1

for i in range(QPS_RANGE_START, QPS_RANGE_END):
    psutil.cpu_percent()
    task_name = "task" + str(next_task)
    thread = threading.Thread(target=benchmark, name=task_name, args=(task_name,))
    thread.start()
    next_task += 1
    time.sleep(QPS_SLEEP)
    cpu_util = psutil.cpu_percent()
    mem_used = psutil.virtual_memory()[3]/1024/1024
    mem_samples.append(mem_used)
    cpu_samples.append(cpu_util)

with open('/tmp/wasm_inf.cpu_util', 'w') as f:
  for entry in cpu_samples:
    f.write(f"{entry}\n")

with open('/tmp/wasm_inf.mem_util', 'w') as f:
  for entry in mem_samples:
    f.write(f"{entry}\n")
