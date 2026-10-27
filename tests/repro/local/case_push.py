import repro, http.server, threading, time, json
server = http.server.ThreadingHTTPServer(('127.0.0.1', 51271), repro.Registry)
server.daemon_threads = True
threading.Thread(target=server.serve_forever, daemon=True).start()
args = ['--provenance=false', '--output', 'type=image,name=127.0.0.1:51271/repro:test,push=true,registry.insecure=true']
results = []
try:
    assert repro.run_case('push-repeat-control', args=args)['returncode'] == 0
    repro.Registry.stall = True
    for i in range(8):
        repro.Registry.activated.clear()

        def cancel(p):
            if not repro.Registry.activated.wait(10):
                raise RuntimeError('upload fault not activated')
            if i == 0:
                time.sleep(10)
                if p.poll() is not None:
                    raise RuntimeError('push did not remain blocked while response withheld')
            p.terminate()
        r = repro.run_case('push-repeat-' + str(i), args=args, action=cancel, marker='pushing layers', limit=10)
        assert r['returncode'] == 130 and (not r['watchdog_killed'])
        results.append(r)
    repro.Registry.stall = False
    results.append(repro.run_case('push-repeat-recovery-before-release', args=args, limit=15))
    assert results[-1]['returncode'] == 0
    (repro.OUT / 'push-repeated-summary.json').write_text(json.dumps(results, indent=2))
finally:
    repro.Registry.release.set()
    server.shutdown()
    server.server_close()
