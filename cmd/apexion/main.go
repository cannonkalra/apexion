// Command apexion is a single-binary data lake exploration tool: it crawls
// object storage, discovers datasets, infers schemas, and catalogs them into an
// embedded DuckDB database you can query locally.
//
// Subcommands:
//
//	apexion serve            start the web UI + API server (and scheduler)
//	apexion crawl <bucket>   run a one-off crawl and exit
//	apexion migrate          apply database migrations and exit
//	apexion version          print version information
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/apexion/apexion/internal/app"
	"github.com/apexion/apexion/internal/config"
)

// Build metadata, injected at link time via -ldflags. See the Makefile.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var cfgPath string
	root := &cobra.Command{
		Use:   "apexion",
		Short: "Explore, catalog, and query data lakes from a single binary",
		Long: `Apexion crawls S3-compatible object storage, discovers datasets, infers their
schemas, and registers them into a local DuckDB catalog you can query with SQL —
no external services, no heavyweight infrastructure. Run it on your laptop or
directly on an EC2 instance next to your data.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "configs/apexion.yaml", "path to config file (optional)")

	root.AddCommand(serveCmd(&cfgPath))
	root.AddCommand(crawlCmd(&cfgPath))
	root.AddCommand(migrateCmd(&cfgPath))
	root.AddCommand(versionCmd())
	return root
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			path = "" // fall back to defaults + env
		}
	}
	return config.Load(path)
}

func serveCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the web UI and API server",
		Long:  "Start the Apexion web UI and REST API (and the background job scheduler). Serves on server.host:server.port (default 0.0.0.0:8080).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			fmt.Printf("apexion %s  ·  data lake exploration, one binary\n", version)
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			a, err := app.New(ctx, cfg)
			if err != nil {
				return err
			}
			return a.Serve(ctx)
		},
	}
}

func crawlCmd(cfgPath *string) *cobra.Command {
	var mode string
	c := &cobra.Command{
		Use:   "crawl <bucket>",
		Short: "Crawl a bucket and catalog its datasets, then exit",
		Long:  "Run a single synchronous crawl of a bucket: list objects, group them into datasets, infer schemas, and persist the results to the catalog.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			ctx := context.Background()
			a, err := app.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer a.Shutdown()
			return a.CrawlOnce(ctx, args[0], mode)
		},
	}
	c.Flags().StringVar(&mode, "mode", "full", "crawl mode: full|incremental")
	return c
}

func migrateCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations and exit",
		Long:  "Apply any pending schema migrations to the DuckDB catalog. Idempotent — safe to run repeatedly. Migrations also run automatically on serve/crawl.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			ctx := context.Background()
			a, err := app.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer a.Shutdown()
			fmt.Println("migrations applied to", cfg.Storage.Path)
			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Printf("Version:    %s\n", version)
			fmt.Printf("Commit:     %s\n", commit)
			fmt.Printf("Built:      %s\n", date)
			fmt.Printf("Go Version: %s\n", runtime.Version())
			fmt.Printf("Platform:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
