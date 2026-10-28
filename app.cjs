const http = require('node:http');
const { setTimeout: delay } = require('node:timers/promises');
const { trace, SpanStatusCode } = require('@opentelemetry/api');
const sdk = require('./instrumentation.cjs');
const tracer = trace.getTracer('playground');
const port = Number(process.env.PORT ?? 3000);

const server = http.createServer(async (req, res) => {
  const path = new URL(req.url, 'http://localhost').pathname;
  const requestSpan = trace.getActiveSpan();
  const traceId = requestSpan?.spanContext().traceId;
  const reply = (status, body) => {
    res.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({ ...body, traceId }) + '\n');
  };
  if (req.method !== 'GET') return reply(405, { error: 'GET only' });
  try {
    if (path === '/ok') return reply(200, { message: 'ok' });
    if (path === '/slow') {
      // HTTPスパンの子として待機区間を明示的に記録する。
      await tracer.startActiveSpan('simulate-work', async (span) => {
        try {
          span.setAttribute('demo.delay_ms', 1000);
          await delay(1000);
        } finally { span.end(); }
      });
      return reply(200, { message: 'slow', delayMs: 1000 });
    }
    if (path === '/error') throw new Error('Intentional demo error');
    return reply(404, { error: 'Use /ok, /slow, or /error' });
  } catch (error) {
    requestSpan?.recordException(error);
    requestSpan?.setStatus({ code: SpanStatusCode.ERROR, message: error.message });
    reply(500, { error: error.message });
  }
});

server.listen(port, '127.0.0.1', () => {
  console.log('Listening on http://127.0.0.1:' + server.address().port);
});
let stopping = false;
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    if (stopping) return;
    stopping = true;
    const timeout = setTimeout(() => process.exit(1), 5000);
    timeout.unref();
    server.close(async () => {
      try { await sdk.shutdown(); }
      catch (error) { console.error(error); process.exitCode = 1; }
      clearTimeout(timeout);
    });
  });
}
