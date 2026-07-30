package ecs

import "slices"

// The cluster dashboard and the per-node dashboard publish some blocks with
// byte-identical keys, and since ADR-0012 they map onto byte-identical metric
// shapes too — the only difference is the family prefix and the identity labels
// the caller scopes them with. Those blocks live here, embedded into both
// response structs, so a payload change is one edit rather than two that can
// drift apart. Same embedded-block + samples() idiom as gcFields and friends.

// transactionFields carries the object-transaction block.
type transactionFields struct {
	TransactionReadLatency             Series `json:"transactionReadLatency"`
	TransactionWriteLatency            Series `json:"transactionWriteLatency"`
	TransactionReadBandwidth           Series `json:"transactionReadBandwidth"`
	TransactionWriteBandwidth          Series `json:"transactionWriteBandwidth"`
	TransactionReadTransactionsPerSec  Series `json:"transactionReadTransactionsPerSec"`
	TransactionWriteTransactionsPerSec Series `json:"transactionWriteTransactionsPerSec"`
}

// samples maps the block onto <prefix>_transaction* names. Read and write are one
// measurement along one dimension, so each pair is one name plus {op} (ADR-0012).
// Missing or unparseable values yield absent samples, never zeros.
func (t transactionFields) samples(prefix string, labels ...Label) []Sample {
	read := append(slices.Clone(labels), Label{"op", "read"})
	write := append(slices.Clone(labels), Label{"op", "write"})

	var out []Sample
	out = appendSeries(out, prefix+"_transaction_latency_milliseconds", t.TransactionReadLatency, read...)
	out = appendSeries(out, prefix+"_transaction_latency_milliseconds", t.TransactionWriteLatency, write...)
	out = appendSeries(out, prefix+"_transaction_bandwidth_mb_per_second", t.TransactionReadBandwidth, read...)
	out = appendSeries(out, prefix+"_transaction_bandwidth_mb_per_second", t.TransactionWriteBandwidth, write...)
	out = appendSeries(out, prefix+"_transactions_per_second", t.TransactionReadTransactionsPerSec, read...)
	out = appendSeries(out, prefix+"_transactions_per_second", t.TransactionWriteTransactionsPerSec, write...)
	return out
}

// diskCountFields carries the disk inventory block.
type diskCountFields struct {
	NumDisks               Num `json:"numDisks"`
	NumGoodDisks           Num `json:"numGoodDisks"`
	NumBadDisks            Num `json:"numBadDisks"`
	NumMaintenanceDisks    Num `json:"numMaintenanceDisks"`
	NumReadyToReplaceDisks Num `json:"numReadyToReplaceDisks"`
}

// samples maps the block onto <prefix>_disks{state} plus <prefix>_disks_installed.
// The population total keeps its own name because the states are not a proven
// partition of it: ECS documents five health states and publishes a count for
// four, so sum(by state) may fall short of installed rather than equal it
// (ADR-0012).
func (d diskCountFields) samples(prefix string, labels ...Label) []Sample {
	var out []Sample
	out = appendNum(out, prefix+"_disks_installed", d.NumDisks, labels...)
	for _, s := range []struct {
		state string
		count Num
	}{
		{"good", d.NumGoodDisks},
		{"bad", d.NumBadDisks},
		{"maintenance", d.NumMaintenanceDisks},
		{"ready_to_replace", d.NumReadyToReplaceDisks},
	} {
		out = appendNum(out, prefix+"_disks", s.count, append(slices.Clone(labels), Label{"state", s.state})...)
	}
	return out
}
