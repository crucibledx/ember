// Local OTLP sink for probing what a harness sends: prints content-type, sizes and span names only.
const port = Number(process.env.PORT ?? 14399)
Bun.serve({
  hostname: '127.0.0.1',
  port,
  async fetch(req) {
    const url = new URL(req.url)
    const body = new Uint8Array(await req.arrayBuffer())
    let names = ''
    if ((req.headers.get('content-type') ?? '').includes('json')) {
      try {
        const j = JSON.parse(new TextDecoder().decode(body))
        names = JSON.stringify(
          (j.resourceSpans ?? []).flatMap((r: any) =>
            r.scopeSpans.flatMap((s: any) => s.spans.map((x: any) => x.name)),
          ),
        )
      } catch {}
    }
    console.log(JSON.stringify({path: url.pathname, ct: req.headers.get('content-type'), bytes: body.length, names}))
    return new Response('{}', {headers: {'content-type': 'application/json'}})
  },
})
console.error(`sink on ${port}`)
