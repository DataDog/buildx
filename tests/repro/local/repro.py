import json, os, pathlib, socket, subprocess, threading, time, uuid, http.server, urllib.parse, hashlib, signal
ROOT = pathlib.Path(os.environ['BUILDX_REPRO_ROOT'])
CTX = ROOT / 'context'
ENV = dict(os.environ, BUILDX_CONFIG=str(ROOT / 'buildx'), TMPDIR=str(ROOT / 'tmp'), DOCKER_HOST='unix:///var/run/docker.sock')
ENV.pop('DOCKER_CONTEXT', None)
SOCK = str(ROOT / 'socket/buildkitd.sock')
OUT = ROOT / 'evidence'
OUT.mkdir(exist_ok=True)
manifest = json.loads((OUT / 'manifest.json').read_text())
owned = json.loads(subprocess.check_output(
    ['docker', 'inspect', manifest['container_id']], env=ENV, text=True))[0]
if owned['Id'] != manifest['container_id'] or owned['Config']['Labels'].get('buildx-local-reproduction') != 'true':
    raise RuntimeError('refusing to use a container not owned by this reproduction run')
if os.environ.get('BUILDX_REPRO_CONTAINER') != manifest['container_name']:
    raise RuntimeError('container name does not match this run manifest')

def run_case(name, mode='ok', args=(), action=None, marker='REPRO_STARTED', limit=45, builder='local-repro'):
    CTX.joinpath('Dockerfile').write_text('FROM scratch\nCOPY repro /repro\nCOPY nonce /nonce\nRUN ["/repro", "' + mode + '"]\n')
    CTX.joinpath('nonce').write_text(str(uuid.uuid4()))
    cmd = ['docker', 'buildx', 'build', '--builder', builder, '--no-cache', '--progress', 'plain', *args, str(CTX)]
    start = time.monotonic()
    p = subprocess.Popen(cmd, env=ENV, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1)
    lines = []
    triggered = threading.Event()

    def collect():
        for line in p.stdout:
            lines.append(line)
            if marker in line:
                triggered.set()
    t = threading.Thread(target=collect, daemon=True)
    t.start()
    injection = None
    action_error = None
    if action:
        if triggered.wait(20):
            injection = round(time.monotonic() - start, 3)
            try:
                action(p)
            except Exception as exc:
                action_error = exc
                p.terminate()
        else:
            p.terminate()
    watchdog = False
    try:
        p.wait(timeout=limit)
    except subprocess.TimeoutExpired:
        watchdog = True
        p.kill()
        p.wait()
    t.join(3)
    result = {'case': name, 'command': cmd, 'returncode': p.returncode, 'elapsed_s': round(time.monotonic() - start, 3), 'injection_s': injection, 'marker_observed': triggered.is_set(), 'watchdog_killed': watchdog}
    (OUT / (name + '.log')).write_text(''.join(lines))
    (OUT / (name + '.json')).write_text(json.dumps(result, indent=2))
    print(json.dumps(result), flush=True)
    if action_error is not None:
        raise RuntimeError('fault injection failed; see retained evidence') from action_error
    if action is not None and not triggered.is_set():
        raise RuntimeError('execution barrier was never reached; fault was not injected')
    return result

def transport_case():
    path = str(ROOT / 'socket/proxy.sock')
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    srv = socket.socket(socket.AF_UNIX)
    srv.bind(path)
    srv.listen()
    connections = []
    stop = threading.Event()

    def forward(a, b):
        try:
            while (data := a.recv(65536)):
                b.sendall(data)
        except OSError:
            pass

    def accept():
        while not stop.is_set():
            try:
                a, _ = srv.accept()
            except OSError:
                break
            b = socket.socket(socket.AF_UNIX)
            b.connect(SOCK)
            connections.extend([a, b])
            threading.Thread(target=forward, args=(a, b), daemon=True).start()
            threading.Thread(target=forward, args=(b, a), daemon=True).start()
    threading.Thread(target=accept, daemon=True).start()
    subprocess.run(['docker', 'buildx', 'create', '--name', 'proxy-repro', '--driver', 'remote', 'unix://' + path], env=ENV, check=True, stdout=subprocess.DEVNULL)

    def cut(p):
        stop.set()
        srv.close()
        for s in connections:
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            s.close()
    try:
        return run_case('transport-eof', 'sleep', action=cut, builder='proxy-repro', limit=20)
    finally:
        cut(None)
        subprocess.run(['docker', 'buildx', 'rm', 'proxy-repro'], env=ENV, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            os.unlink(path)
        except FileNotFoundError:
            pass

class Registry(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    blobs = {}
    uploads = {}
    manifests = {}
    stall = False
    activated = threading.Event()
    release = threading.Event()
    events = []

    def handle_one_request(self):
        if self.rfile.peek(1)[:1] == b'\x16':
            self.connection.sendall(b'HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
            self.close_connection = True
            return
        return super().handle_one_request()

    def log_message(self, *a):
        pass

    def body(self):
        if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
            data = b''
            while True:
                n = int(self.rfile.readline().split(b';')[0], 16)
                if not n:
                    self.rfile.readline()
                    break
                data += self.rfile.read(n)
                self.rfile.read(2)
            return data
        return self.rfile.read(int(self.headers.get('Content-Length', '0')))

    def response(self, status, body=b'', **headers):
        self.send_response(status)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Docker-Distribution-Api-Version', 'registry/2.0')
        for k, v in headers.items():
            self.send_header(k.replace('_', '-'), v)
        self.end_headers()
        if self.command != 'HEAD':
            try:
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                pass

    def do_GET(self):
        if self.path in ['/v2/', '/v2']:
            return self.response(200, b'{}')
        if '/blobs/' in self.path:
            d = self.path.rsplit('/', 1)[-1]
            if d in self.blobs:
                return self.response(200, self.blobs[d], Docker_Content_Digest=d)
        return self.response(404, b'{"errors":[{"code":"BLOB_UNKNOWN","message":"unknown"}]}', Content_Type='application/json')

    def do_HEAD(self):
        self.do_GET()

    def do_POST(self):
        self.events.append((self.command, self.path, time.time()))
        u = str(uuid.uuid4())
        self.uploads[u] = self.body()
        return self.response(202, Location=self.path.rstrip('/') + '/' + u, Docker_Upload_UUID=u, Range='0-0')

    def do_PATCH(self):
        self.events.append((self.command, self.path, time.time()))
        u = urllib.parse.urlparse(self.path).path.rsplit('/', 1)[-1]
        self.uploads[u] = self.uploads.get(u, b'') + self.body()
        return self.response(202, Location=self.path, Range='0-' + str(max(0, len(self.uploads[u]) - 1)))

    def do_PUT(self):
        self.events.append((self.command, self.path, time.time()))
        parsed = urllib.parse.urlparse(self.path)
        data = self.body()
        if '/blobs/uploads/' in parsed.path:
            u = parsed.path.rsplit('/', 1)[-1]
            data = self.uploads.get(u, b'') + data
            digest = urllib.parse.parse_qs(parsed.query).get('digest', [''])[0]
            if digest != 'sha256:' + hashlib.sha256(data).hexdigest():
                return self.response(400)
            if self.stall:
                self.activated.set()
                self.release.wait(60)
            self.blobs[digest] = data
            return self.response(201, Location='/v2/repro/blobs/' + digest, Docker_Content_Digest=digest)
        if '/manifests/' in parsed.path:
            digest = 'sha256:' + hashlib.sha256(data).hexdigest()
            self.manifests[parsed.path] = data
            return self.response(201, Docker_Content_Digest=digest)
        self.response(404)

def push_cases():
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 51271), Registry)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    args = ['--provenance=false', '--output', 'type=image,name=127.0.0.1:51271/repro:test,push=true,registry.insecure=true']
    try:
        assert run_case('push-control', args=args)['returncode'] == 0, 'healthy push must pass before injection'
        Registry.stall = True

        def cancel(p):
            if Registry.activated.wait(15):
                p.terminate()
        run_case('push-stall-cancel', args=args, action=cancel, marker='pushing layers', limit=20)
        Registry.release.set()
        Registry.stall = False
        run_case('push-after-cancel', args=args)
        (OUT / 'registry-events.json').write_text(json.dumps(Registry.events, indent=2))
    finally:
        Registry.release.set()
        server.shutdown()
        server.server_close()
