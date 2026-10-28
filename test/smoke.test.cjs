const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { once } = require('node:events');

test('HTTP responses and exported traces', { timeout: 15000 }, async () => {
  const child = spawn(process.execPath, ['--require', './instrumentation.cjs', 'app.cjs'], {
    cwd: require('node:path').resolve(__dirname, '..'),
    env: { ...process.env, PORT: '0' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const exited = once(child, 'exit');
  let output = '';
  let errors = '';
  child.stderr.on('data', chunk => { errors += chunk; });
  let readyResolve;
  const ready = new Promise(resolve => { readyResolve = resolve; });
  child.stdout.on('data', chunk => {
    output += chunk;
    const match = output.match(/Listening on (http:\/\/127\.0\.0\.1:\d+)/);
    if (match) readyResolve(match[1]);
  });
  const timer = setTimeout(() => child.kill('SIGKILL'), 12000);
  try {
    const base = await Promise.race([ready, exited.then(() => { throw new Error(errors || output); })]);
    const ids = [];
    for (const [path, status] of [['/ok', 200], ['/slow', 200], ['/error', 500]]) {
      const start = performance.now();
      const response = await fetch(base + path);
      const body = await response.json();
      assert.equal(response.status, status);
      assert.match(body.traceId, /^[0-9a-f]{32}$/);
      assert.notEqual(body.traceId, '0'.repeat(32));
      ids.push(body.traceId);
      if (path === '/slow') assert.ok(performance.now() - start >= 950);
    }
    child.kill('SIGTERM');
    const [code] = await exited;
    assert.equal(code, 0, errors);
    for (const id of ids) assert.ok(output.includes(id), 'trace must be exported');
    assert.match(output, /simulate-work/);
    assert.match(output, /exception/);
    assert.match(output, /code: 2/);
  } finally {
    clearTimeout(timer);
    if (child.exitCode === null) child.kill('SIGKILL');
  }
});
