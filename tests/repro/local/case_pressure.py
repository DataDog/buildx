import repro, pathlib, subprocess, threading, time, json, shutil
src = repro.CTX / 'repro.c'
s = src.read_text()
if '#include <time.h>' not in s:
    s = s.replace('#include <unistd.h>', '#include <unistd.h>\n#include <time.h>')
if '"cpu"' not in s:
    s = s.replace('  puts("REPRO_OK");', '  if(argc>1 && !strcmp(argv[1],"cpu")){time_t end=time(NULL)+12;volatile unsigned long x=1;while(time(NULL)<end){for(int i=0;i<10000;i++)x=x*1664525+1013904223;}}\n  puts("REPRO_OK");')
src.write_text(s)
subprocess.run(['cc', '-static', '-O0', '-o', str(repro.CTX / 'repro'), str(src)], env=repro.ENV, check=True)
name = __import__('os').environ['BUILDX_REPRO_CONTAINER']
processes = []
files = []
results = []
cmd = [str(repro.ROOT / 'buildctl'), '--addr', 'unix://' + repro.SOCK, 'debug', 'workers']
try:
    subprocess.run(['docker', 'update', '--cpus', '0.1', name], check=True, capture_output=True)
    for i in range(4):
        ctx = repro.ROOT / ('cpu-context-' + str(i))
        ctx.mkdir(exist_ok=True)
        shutil.copy2(repro.CTX / 'repro', ctx / 'repro')
        (ctx / 'nonce').write_text(str(i) + str(time.time_ns()))
        (ctx / 'Dockerfile').write_text('FROM scratch\nCOPY repro /repro\nCOPY nonce /nonce\nRUN ["/repro","cpu"]\n')
        f = (repro.OUT / ('cpu-build-' + str(i) + '.log')).open('w')
        files.append(f)
        processes.append(subprocess.Popen(['docker', 'buildx', 'build', '--builder', 'local-repro', '--no-cache', '--progress', 'plain', str(ctx)], env=repro.ENV, stdout=f, stderr=subprocess.STDOUT))
    for i in range(6):
        t = time.monotonic()
        try:
            p = subprocess.run(cmd, capture_output=True, text=True, timeout=5, env=repro.ENV)
            results.append({'probe': i, 'elapsed_s': round(time.monotonic() - t, 3), 'timeout': False, 'returncode': p.returncode})
        except subprocess.TimeoutExpired:
            results.append({'probe': i, 'elapsed_s': round(time.monotonic() - t, 3), 'timeout': True})
        time.sleep(1)
finally:
    subprocess.run(['docker', 'update', '--cpus', '2', name], check=True, capture_output=True)
    for p in processes:
        try:
            p.wait(timeout=20)
        except subprocess.TimeoutExpired:
            p.terminate()
            p.wait(timeout=10)
    for f in files:
        f.close()
(repro.OUT / 'cpu-pressure-probes.json').write_text(json.dumps({'cpu_limit': 0.1, 'concurrent_builds': 4, 'probes': results, 'build_exits': [p.returncode for p in processes]}, indent=2))
print(json.dumps({'probes': results, 'build_exits': [p.returncode for p in processes]}), flush=True)
