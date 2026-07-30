package ecs

import (
	"context"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

const pathLocalZone = "/dashboard/zones/localzone"

// localZoneResp models GET /dashboard/zones/localzone (OBS 4.1). Counts arrive as
// quoted strings; stats are time-series arrays (the newest point is "current").
type localZoneResp struct {
	Name string `json:"name"`

	NumNodes            Num `json:"numNodes"`
	NumGoodNodes        Num `json:"numGoodNodes"`
	NumBadNodes         Num `json:"numBadNodes"`
	NumMaintenanceNodes Num `json:"numMaintenanceNodes"`

	NumDisks               Num `json:"numDisks"`
	NumGoodDisks           Num `json:"numGoodDisks"`
	NumBadDisks            Num `json:"numBadDisks"`
	NumMaintenanceDisks    Num `json:"numMaintenanceDisks"`
	NumReadyToReplaceDisks Num `json:"numReadyToReplaceDisks"`

	AlertsNumUnackCritical Series `json:"alertsNumUnackCritical"`
	AlertsNumUnackError    Series `json:"alertsNumUnackError"`
	AlertsNumUnackInfo     Series `json:"alertsNumUnackInfo"`
	AlertsNumUnackWarning  Series `json:"alertsNumUnackWarning"`

	DiskSpaceTotalCurrent        Series `json:"diskSpaceTotalCurrent"`
	DiskSpaceFreeCurrent         Series `json:"diskSpaceFreeCurrent"`
	DiskSpaceAllocatedCurrent    Series `json:"diskSpaceAllocatedCurrent"`
	DiskSpaceReservedCurrent     Series `json:"diskSpaceReservedCurrent"`
	DiskSpaceOfflineTotalCurrent Series `json:"diskSpaceOfflineTotalCurrent"`

	TransactionReadLatency             Series `json:"transactionReadLatency"`
	TransactionWriteLatency            Series `json:"transactionWriteLatency"`
	TransactionReadBandwidth           Series `json:"transactionReadBandwidth"`
	TransactionWriteBandwidth          Series `json:"transactionWriteBandwidth"`
	TransactionReadTransactionsPerSec  Series `json:"transactionReadTransactionsPerSec"`
	TransactionWriteTransactionsPerSec Series `json:"transactionWriteTransactionsPerSec"`

	TransactionErrors struct {
		ErrorSuccessTotals []struct {
			SuccessTotal Num `json:"successTotal"`
			ErrorTotal   Num `json:"errorTotal"`
		} `json:"errorSuccessTotals"`
		Types []struct {
			ErrorType  string `json:"errorType"`
			Category   string `json:"category"`
			ErrorCount Num    `json:"errorCount"`
		} `json:"types"`
	} `json:"transactionErrors"`

	// Real clusters return these as time-series arrays ([{"t":…,"Bandwidth":…}]),
	// not scalars — confirmed live against 4.3. The per-RG equivalents in
	// replication.go are already Series; the cluster-level ones were mistyped Num
	// and silently dropped.
	ReplicationIngressTrafficCurrent Series `json:"replicationIngressTrafficCurrent"`
	ReplicationEgressTrafficCurrent  Series `json:"replicationEgressTrafficCurrent"`

	ReplicationRpoLag       Num `json:"replicationRpoLag"`
	ReplicationRpoTimestamp Num `json:"replicationRpoTimestamp"`

	gcFields
	recoveryFields
	erasureCodingFields
	allocationComponentFields
}

// Cluster collects VDC-wide health, capacity, and transaction stats from the
// local-zone dashboard endpoint.
type Cluster struct{}

// Name identifies this collector in ecs_collector_up.
func (Cluster) Name() string { return "cluster" }

// Collect fetches /dashboard/zones/localzone and maps it to samples.
func (Cluster) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var z localZoneResp
	if err := c.Get(ctx, pathLocalZone, &z); err != nil {
		return nil, err
	}

	var out []Sample
	// The per-state counts share one metric name; the population total keeps its
	// own (_installed) because the states are NOT a proven partition of it — ECS
	// documents five health states and publishes a count for only three, so
	// sum(ecs_cluster_nodes) may fall short of the installed count rather than
	// equal it. See ADR-0012.
	out = appendNum(out, "ecs_cluster_nodes_installed", z.NumNodes)
	out = appendNum(out, "ecs_cluster_nodes", z.NumGoodNodes, Label{"state", "good"})
	out = appendNum(out, "ecs_cluster_nodes", z.NumBadNodes, Label{"state", "bad"})
	out = appendNum(out, "ecs_cluster_nodes", z.NumMaintenanceNodes, Label{"state", "maintenance"})

	out = appendNum(out, "ecs_cluster_disks_installed", z.NumDisks)
	out = appendNum(out, "ecs_cluster_disks", z.NumGoodDisks, Label{"state", "good"})
	out = appendNum(out, "ecs_cluster_disks", z.NumBadDisks, Label{"state", "bad"})
	out = appendNum(out, "ecs_cluster_disks", z.NumMaintenanceDisks, Label{"state", "maintenance"})
	out = appendNum(out, "ecs_cluster_disks", z.NumReadyToReplaceDisks, Label{"state", "ready_to_replace"})

	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackCritical, Label{"severity", "critical"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackError, Label{"severity", "error"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackInfo, Label{"severity", "info"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackWarning, Label{"severity", "warning"})

	// allocated + free + reserved partitions the total exactly (verified to the
	// byte on a live 4.3 cluster), so the total must stay outside the labeled
	// family or sum(ecs_cluster_disk_space_bytes) would double it. Offline space
	// is not part of that partition and keeps its own name too.
	out = appendSeries(out, "ecs_cluster_disk_space_total_bytes", z.DiskSpaceTotalCurrent)
	out = appendSeries(out, "ecs_cluster_disk_space_bytes", z.DiskSpaceAllocatedCurrent, Label{"type", "allocated"})
	out = appendSeries(out, "ecs_cluster_disk_space_bytes", z.DiskSpaceFreeCurrent, Label{"type", "free"})
	out = appendSeries(out, "ecs_cluster_disk_space_bytes", z.DiskSpaceReservedCurrent, Label{"type", "reserved"})
	out = appendSeries(out, "ecs_cluster_disk_space_offline_total_bytes", z.DiskSpaceOfflineTotalCurrent)

	read, write := Label{"op", "read"}, Label{"op", "write"}
	out = appendSeries(out, "ecs_cluster_transaction_latency_milliseconds", z.TransactionReadLatency, read)
	out = appendSeries(out, "ecs_cluster_transaction_latency_milliseconds", z.TransactionWriteLatency, write)
	out = appendSeries(out, "ecs_cluster_transaction_bandwidth_mb_per_second", z.TransactionReadBandwidth, read)
	out = appendSeries(out, "ecs_cluster_transaction_bandwidth_mb_per_second", z.TransactionWriteBandwidth, write)
	out = appendSeries(out, "ecs_cluster_transactions_per_second", z.TransactionReadTransactionsPerSec, read)
	out = appendSeries(out, "ecs_cluster_transactions_per_second", z.TransactionWriteTransactionsPerSec, write)

	if len(z.TransactionErrors.ErrorSuccessTotals) > 0 {
		totals := z.TransactionErrors.ErrorSuccessTotals[0]
		out = appendNum(out, "ecs_cluster_transactions_total", totals.ErrorTotal, Label{"outcome", "error"})
		out = appendNum(out, "ecs_cluster_transactions_total", totals.SuccessTotal, Label{"outcome", "success"})
	}
	for _, te := range z.TransactionErrors.Types {
		if !te.ErrorCount.Set {
			continue
		}
		code, proto := splitErrorType(te.ErrorType)
		out = append(out, Sample{
			Name: "ecs_cluster_transaction_errors",
			Labels: []Label{
				{Key: "code", Value: code},
				{Key: "protocol", Value: proto},
				{Key: "category", Value: te.Category},
			},
			Value: te.ErrorCount.Val,
		})
	}

	out = appendSeries(out, "ecs_cluster_replication_traffic", z.ReplicationIngressTrafficCurrent, Label{"direction", "ingress"})
	out = appendSeries(out, "ecs_cluster_replication_traffic", z.ReplicationEgressTrafficCurrent, Label{"direction", "egress"})

	out = appendNum(out, "ecs_cluster_replication_rpo_lag_seconds", z.ReplicationRpoLag)
	out = appendNum(out, "ecs_cluster_replication_rpo_timestamp_seconds", z.ReplicationRpoTimestamp)

	out = append(out, z.gcFields.samples()...)
	out = append(out, z.recoveryFields.samples()...)
	out = append(out, z.erasureCodingFields.samples()...)
	out = append(out, z.allocationComponentFields.samples()...)

	return out, nil
}

// splitErrorType parses the dashboard's combined error key, e.g. "403 (S3)" →
// ("403", "S3"). Unparseable values keep the whole string as the code.
func splitErrorType(s string) (code, proto string) {
	fields := strings.Fields(s)
	if len(fields) >= 2 {
		return fields[0], strings.Trim(fields[1], "()")
	}
	return strings.TrimSpace(s), ""
}
