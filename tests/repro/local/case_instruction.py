import repro
assert repro.run_case('healthy')['returncode'] == 0
result = repro.run_case('instruction-exit42', 'fail')
assert result['returncode'] != 0 and (not result['watchdog_killed'])
assert 'exit code: 42' in (repro.OUT / 'instruction-exit42.log').read_text()
assert repro.run_case('user-cancellation', 'sleep', action=lambda p: p.terminate(), limit=15)['returncode'] == 130
assert repro.run_case('runner-hard-kill', 'sleep', action=lambda p: p.kill(), limit=5)['returncode'] == -9
assert repro.run_case('healthy-after-cancellation')['returncode'] == 0
