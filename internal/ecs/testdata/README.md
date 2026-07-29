# Test fixtures

`localzone.json`, `nodes.json`, `replicationgroups.json`, `vdc-nodes.json`,
`namespaces.json`, `billing.json`, `quota-*.json` are derived from the Dell
ObjectScale 4.1 REST reference examples, with targeted corrections where a live
4.3 cluster contradicted the reference (see ADR-0007). Values are chosen to be
distinct and non-zero, and some fields are deliberately omitted or made
unparseable so the suite can tell an absent sample from a zero one. Their copies
under `cmd/mockecs/fixtures/` must stay byte-identical.

`localzone-live-4.3.json` is different: it is an unedited capture of
`GET /dashboard/zones/localzone` from a real ObjectScale 4.3.0.0 cluster,
contributed as a sanitized trace on PR #18, with only `name` normalized. It is
read by exactly one test (`cluster_livepayload_test.go`), which asserts **shape,
never values** — the cluster was idle, so most values are zero and would make
weak assertions. Its job is to catch a misspelled JSON tag against an authentic
payload, which hand-written fixtures structurally cannot do. Do not add it to
`mockClient`, and do not copy it to `cmd/mockecs/fixtures/`.
