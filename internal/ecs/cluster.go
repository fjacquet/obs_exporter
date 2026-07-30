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
	out = appendNum(out, "ecs_cluster_nodes", z.NumNodes)
	out = appendNum(out, "ecs_cluster_good_nodes", z.NumGoodNodes)
	out = appendNum(out, "ecs_cluster_bad_nodes", z.NumBadNodes)
	out = appendNum(out, "ecs_cluster_maintenance_nodes", z.NumMaintenanceNodes)

	out = appendNum(out, "ecs_cluster_disks", z.NumDisks)
	out = appendNum(out, "ecs_cluster_good_disks", z.NumGoodDisks)
	out = appendNum(out, "ecs_cluster_bad_disks", z.NumBadDisks)
	out = appendNum(out, "ecs_cluster_maintenance_disks", z.NumMaintenanceDisks)
	out = appendNum(out, "ecs_cluster_ready_to_replace_disks", z.NumReadyToReplaceDisks)

	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackCritical, Label{"severity", "critical"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackError, Label{"severity", "error"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackInfo, Label{"severity", "info"})
	out = appendSeries(out, "ecs_cluster_alerts_unacknowledged", z.AlertsNumUnackWarning, Label{"severity", "warning"})

	out = appendSeries(out, "ecs_cluster_disk_space_total_bytes", z.DiskSpaceTotalCurrent)
	out = appendSeries(out, "ecs_cluster_disk_space_free_bytes", z.DiskSpaceFreeCurrent)
	out = appendSeries(out, "ecs_cluster_disk_space_allocated_bytes", z.DiskSpaceAllocatedCurrent)
	out = appendSeries(out, "ecs_cluster_disk_space_reserved_bytes", z.DiskSpaceReservedCurrent)
	out = appendSeries(out, "ecs_cluster_disk_space_offline_total_bytes", z.DiskSpaceOfflineTotalCurrent)

	out = appendSeries(out, "ecs_cluster_transaction_read_latency_milliseconds", z.TransactionReadLatency)
	out = appendSeries(out, "ecs_cluster_transaction_write_latency_milliseconds", z.TransactionWriteLatency)
	out = appendSeries(out, "ecs_cluster_transaction_read_bandwidth_mb_per_second", z.TransactionReadBandwidth)
	out = appendSeries(out, "ecs_cluster_transaction_write_bandwidth_mb_per_second", z.TransactionWriteBandwidth)
	out = appendSeries(out, "ecs_cluster_transactions_read_per_second", z.TransactionReadTransactionsPerSec)
	out = appendSeries(out, "ecs_cluster_transactions_write_per_second", z.TransactionWriteTransactionsPerSec)

	if len(z.TransactionErrors.ErrorSuccessTotals) > 0 {
		out = appendNum(out, "ecs_cluster_transaction_errors_total", z.TransactionErrors.ErrorSuccessTotals[0].ErrorTotal)
		out = appendNum(out, "ecs_cluster_transaction_successes_total", z.TransactionErrors.ErrorSuccessTotals[0].SuccessTotal)
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

	out = appendSeries(out, "ecs_cluster_replication_ingress_traffic", z.ReplicationIngressTrafficCurrent)
	out = appendSeries(out, "ecs_cluster_replication_egress_traffic", z.ReplicationEgressTrafficCurrent)

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
