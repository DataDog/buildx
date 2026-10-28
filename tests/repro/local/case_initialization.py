from pathlib import Path
import socket, subprocess, os, threading, time, json
import repro
r = repro.ROOT
sock = str(r / 'socket/stalled.sock')
try:
    os.unlink(sock)
except FileNotFoundError:
    pass
srv = socket.socket(socket.AF_UNIX)
srv.bind(sock)
srv.listen()
connections = []
accepted = threading.Event()

def accept():
    while True:
        try:
            s, _ = srv.accept()
        except OSError:
            return
        connections.append(s)
        accepted.set()
threading.Thread(target=accept, daemon=True).start()
cmd = ['docker', 'buildx', 'create', '--name', 'stalled-repro', '--driver', 'remote', '--bootstrap', '--timeout=2s', 'unix://' + sock]
t = time.monotonic()
try:
    p = subprocess.run(cmd, env=repro.ENV, capture_output=True, text=True, timeout=75)
    result = {'returncode': p.returncode, 'elapsed_s': round(time.monotonic() - t, 3), 'accepted_connection': accepted.is_set(), 'command': cmd}
    (repro.OUT / 'initialization-deadline.log').write_text(p.stdout + p.stderr)
    (repro.OUT / 'initialization-deadline.json').write_text(json.dumps(result, indent=2))
    print(json.dumps(result))
    assert result.get('returncode') == 1 and result['accepted_connection']
    assert 'context deadline exceeded' in p.stdout + p.stderr
except subprocess.TimeoutExpired as e:
    result = {'watchdog_killed': True, 'elapsed_s': round(time.monotonic() - t, 3), 'accepted_connection': accepted.is_set(), 'command': cmd}
    (repro.OUT / 'initialization-deadline.log').write_bytes((e.stdout or b'') + (e.stderr or b''))
    (repro.OUT / 'initialization-deadline.json').write_text(json.dumps(result, indent=2))
    print(json.dumps(result))
    raise RuntimeError('initialization watchdog expired; expected deadline was not observed') from e
finally:
    srv.close()
    for s in connections:
        s.close()
    subprocess.run(['docker', 'buildx', 'rm', 'stalled-repro'], env=repro.ENV, capture_output=True)
    os.unlink(sock)
