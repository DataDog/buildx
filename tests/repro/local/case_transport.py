import repro
assert repro.run_case('healthy')['returncode'] == 0
result = repro.transport_case()
assert result['returncode'] != 0 and (not result['watchdog_killed'])
assert 'error reading from server: EOF' in (repro.OUT / 'transport-eof.log').read_text()
assert repro.run_case('healthy-after-transport')['returncode'] == 0
