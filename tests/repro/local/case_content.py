import repro, threading, http.server, json, hashlib, io, tarfile, subprocess, time

class ContentRegistry(repro.Registry):
    corrupt = False
    digest = ''
    good = b''

    def do_GET(self):
        parsed = self.path.split('?', 1)[0]
        if '/manifests/' in parsed and parsed in self.manifests:
            body = self.manifests[parsed]
            return self.response(200, body, Content_Type='application/vnd.oci.image.manifest.v1+json', Docker_Content_Digest='sha256:' + hashlib.sha256(body).hexdigest())
        if '/blobs/' in parsed and parsed.rsplit('/', 1)[-1] == self.digest:
            body = self.good
            print(json.dumps({'request': self.path, 'range': self.headers.get('Range'), 'corrupt': self.corrupt}), flush=True)
            if self.corrupt:
                body = body[:512] + b'B' + body[513:]
            return self.response(200, body, Content_Type='application/octet-stream', Docker_Content_Digest=self.digest)
        return super().do_GET()
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode='w') as tar:
    info = tarfile.TarInfo('fixture-' + __import__('uuid').uuid4().hex)
    info.size = 1
    info.mode = 420
    tar.addfile(info, io.BytesIO(b'A'))
layer = buf.getvalue()
digest = 'sha256:' + hashlib.sha256(layer).hexdigest()
config = json.dumps({'architecture': {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}[__import__('platform').machine()], 'os': 'linux', 'rootfs': {'type': 'layers', 'diff_ids': [digest]}, 'config': {}}, separators=(',', ':')).encode()
cd = 'sha256:' + hashlib.sha256(config).hexdigest()
manifest = json.dumps({'schemaVersion': 2, 'mediaType': 'application/vnd.oci.image.manifest.v1+json', 'config': {'mediaType': 'application/vnd.oci.image.config.v1+json', 'digest': cd, 'size': len(config)}, 'layers': [{'mediaType': 'application/vnd.oci.image.layer.v1.tar', 'digest': digest, 'size': len(layer)}]}, separators=(',', ':')).encode()
ContentRegistry.blobs = {cd: config}
ContentRegistry.manifests = {'/v2/repro/manifests/content': manifest}
ContentRegistry.manifests['/v2/repro/manifests/sha256:' + hashlib.sha256(manifest).hexdigest()] = manifest
ContentRegistry.digest = digest
ContentRegistry.good = layer
srv = http.server.ThreadingHTTPServer(('127.0.0.1', 51271), ContentRegistry)
srv.daemon_threads = True
threading.Thread(target=srv.serve_forever, daemon=True).start()
try:
    repro.run_case('missing-cache-import', args=['--cache-from', 'type=registry,ref=127.0.0.1:51271/repro:missing'])
    for name, corrupt in [('content-healthy-after-restart', False)] if __import__('sys').argv[1:] == ['healthy'] else [('content-digest-mismatch', True), ('content-healthy-control', False), ('content-healthy-delay2', False), ('content-healthy-delay10', False)]:
        if 'delay2' in name:
            time.sleep(2)
        if 'delay10' in name:
            time.sleep(10)
        ContentRegistry.corrupt = corrupt
        repro.CTX.joinpath('Dockerfile').write_text('FROM 127.0.0.1:51271/repro:content\nCOPY repro /repro\nRUN ["/repro","ok"]\n')
        cmd = ['docker', 'buildx', 'build', '--builder', 'local-repro', '--no-cache', '--progress', 'plain', str(repro.CTX)]
        t = time.monotonic()
        p = subprocess.run(cmd, env=repro.ENV, capture_output=True, text=True, timeout=40)
        result = {'case': name, 'returncode': p.returncode, 'elapsed_s': round(time.monotonic() - t, 3), 'corruption_enabled': corrupt, 'expected_digest': digest}
        (repro.OUT / (name + '.log')).write_text(p.stdout + p.stderr)
        (repro.OUT / (name + '.json')).write_text(json.dumps(result, indent=2))
        print(json.dumps(result), flush=True)
finally:
    srv.shutdown()
    srv.server_close()
assert 'unexpected commit digest' in (repro.OUT / 'content-digest-mismatch.log').read_text()
assert json.loads((repro.OUT / 'missing-cache-import.json').read_text())['returncode'] == 0
assert json.loads((repro.OUT / 'content-healthy-delay10.json').read_text())['returncode'] == 0
