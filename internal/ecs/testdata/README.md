# Test fixtures

`localzone.json`, `nodes.json`, `replicationgroups.json`, `vdc-nodes.json`,
`namespaces.json`, `billing.json`, `quota-*.json` are derived from the Dell
ObjectScale 4.1 REST reference examples, with targeted corrections where a live
4.3 cluster contradicted the reference (see ADR-0007). Values are chosen to be
distinct and non-zero, and some fields are deliberately omitted or made
unparseable so the suite can tell an absent sample from a zero one. Their copies
under `cmd/mockecs/fixtures/` must stay byte-identical — `fixtures_sync_test.go`
enforces that.

`localzone-live-4.3.json` is different: it holds the real response
`GET /dashboard/zones/localzone` returned by an ObjectScale 4.3.0.0 cluster. It
is **not** a file from PR #18 — it was extracted from a sanitized `Trace` log
that the PR #18 contributor supplied separately.

What was changed on the way in, and nothing else: the extraction script
re-serialized the JSON with sorted keys and two-space indentation, and normalized
`name` to `vdc-example`. Every field name and every value is otherwise exactly
what the cluster returned, which is the whole point — those are what a misspelled
JSON struct tag has to survive, and hand-written fixtures cannot test that
because they carry the same misspelling as the code. The script also asserts the
payload contains no UUID and no build hash before writing it.

It is read by exactly one test (`cluster_livepayload_test.go`), which asserts
**shape, never values**: the cluster was idle, so most values are zero and would
make weak assertions. Do not add it to `mockClient`, and do not copy it to
`cmd/mockecs/fixtures/`.

## Flux fixtures (`flux_*.json`)

Real `POST /flux/api/external/v2/query` responses captured on an ObjectScale
4.3.0.0.142978 acceptance cluster on 2026-07-31, verbatim except for host
identifiers: the capture's already-pseudonymous `node-1`/`node-2` and
`192.168.2.1`/`192.168.2.2` are remapped onto this repo's demo inventory
(`supr01-r01`/`supr01-r02`, `10.1.0.1`/`10.1.0.2`) and rows for the capture's
other three nodes are dropped, so `make demo` shows one coherent cluster rather
than two disjoint node sets. `flux_net.json` carries one extra row under
`not-in-this-cluster.example.com` so the unmapped-host counter has something to
count.

`flux_cq_transaction.json` and `flux_cq_throughput.json` are **synthesized**:
their measurements are confirmed in prose with no attached payload. Each says so
in a `_comment` key. Replace them when a real capture arrives.

`flux_empty.json` is the live answer to a measurement the store does not carry —
HTTP 200 with `Series:[{Datatypes:null,Columns:null,Values:null}]`.
