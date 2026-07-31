package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecs"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	"github.com/spf13/cobra"
)

// defaultFluxCaptureDir is flux-capture's default --out directory. It lives
// inside the repo working tree for convenience, so it is gitignored (see
// .gitignore): the files it writes carry real cluster hostnames, IPs and
// namespace names, and a stray `git add -A` must not commit them.
const defaultFluxCaptureDir = "flux-capture"

// fluxCaptureCmd replays the collector's own query table once and writes each
// response to its own file.
//
// It exists because the live-cluster reporter is reachable by email on a
// months-long round trip, and assembling this by hand is what made the last
// capture a campaign. The output is deliberately raw: sanitizing is the
// reporter's call, on their data policy, and half-done automatic redaction is
// worse than none.
func fluxCaptureCmd() *cobra.Command {
	var cfgPath, cluster, outDir, bucket, measurement string
	var trace bool
	cmd := &cobra.Command{
		Use:   "flux-capture",
		Short: "Query the Flux store once and write each measurement's raw response to a file",
		Long: "Runs the exporter's own Flux query table against one configured cluster and " +
			"writes one JSON file per measurement, plus a summary. Use --bucket and " +
			"--measurement together to probe a measurement the table does not carry. " +
			"Responses are written verbatim: sanitize before sharing them.\n\n" +
			"The default --out directory (\"" + defaultFluxCaptureDir + "\") is gitignored: " +
			"its files carry real cluster hostnames, IPs and namespace names, and must " +
			"never be committed.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (bucket == "") != (measurement == "") {
				return fmt.Errorf("--bucket and --measurement must be given together (got --bucket=%q --measurement=%q)", bucket, measurement)
			}
			config.LoadDotEnv(cfgPath)
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			target, err := selectCluster(cfg, cluster)
			if err != nil {
				return fmt.Errorf("%w (config: %s)", err, cfgPath)
			}
			if err := os.MkdirAll(outDir, 0o750); err != nil {
				return err
			}

			// Mirrors buildTargets (main.go): same client config fields, same BaseURL
			// construction, same TLS handling.
			c := ecsclient.NewClusterClient(ecsclient.Config{
				Name:               target.Name,
				BaseURL:            target.BaseURL(),
				Username:           target.Username,
				Password:           target.Password,
				InsecureSkipVerify: target.InsecureSkipVerify.Bool(),
				Trace:              trace,
			})
			defer func() { _ = c.Close() }()

			scripts := ecs.FluxScripts()
			if bucket != "" && measurement != "" {
				scripts = map[string]string{
					bucket + "/" + measurement: ecs.FluxScriptFor(bucket, measurement),
				}
			}

			type result struct {
				Key   string `json:"measurement"`
				Query string `json:"query"`
				Rows  int    `json:"rows"`
				Err   string `json:"error,omitempty"`
			}
			var summary []result
			for key, script := range scripts {
				var raw json.RawMessage
				r := result{Key: key, Query: script}
				if err := c.Post(cmd.Context(), ecs.FluxPath,
					map[string]string{"query": script}, &raw); err != nil {
					r.Err = err.Error()
					summary = append(summary, r)
					fmt.Fprintf(os.Stderr, "%s: %v\n", key, err)
					continue
				}
				name := strings.ReplaceAll(key, "/", "-") + ".json"
				if err := os.WriteFile(filepath.Join(outDir, name), raw, 0o600); err != nil {
					return err
				}
				r.Rows = countRows(raw)
				summary = append(summary, r)
				fmt.Printf("%s: %d rows -> %s\n", key, r.Rows, name)
			}
			b, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(outDir, "summary.json"), b, 0o600)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config file")
	cmd.Flags().StringVar(&cluster, "cluster", "", "cluster name (default: the first configured)")
	cmd.Flags().StringVar(&outDir, "out", defaultFluxCaptureDir, "directory to write responses into")
	cmd.Flags().StringVar(&bucket, "bucket", "", "probe one measurement: its bucket")
	cmd.Flags().StringVar(&measurement, "measurement", "", "probe one measurement: its name")
	cmd.Flags().BoolVar(&trace, "trace", false, "log every management API response body")
	return cmd
}

// selectCluster picks the cluster to capture from: the named cluster when
// name is non-empty, otherwise the first configured cluster. A name that
// matches nothing is a clear error rather than a silent fallback to the
// first cluster — unlike the naive `Name == cluster || cluster == ""` loop,
// which would keep matching every remaining cluster against an empty name.
func selectCluster(cfg *config.Config, name string) (*config.Cluster, error) {
	if name == "" {
		if len(cfg.Clusters) == 0 {
			return nil, fmt.Errorf("no clusters configured")
		}
		return &cfg.Clusters[0], nil
	}
	for i := range cfg.Clusters {
		if cfg.Clusters[i].Name == name {
			return &cfg.Clusters[i], nil
		}
	}
	return nil, fmt.Errorf("no cluster named %q", name)
}

// countRows reports how many rows a raw envelope carried, for the summary.
func countRows(raw []byte) int {
	var env struct {
		Series []struct {
			Values [][]json.RawMessage `json:"Values"`
		} `json:"Series"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return 0
	}
	n := 0
	for _, s := range env.Series {
		n += len(s.Values)
	}
	return n
}
