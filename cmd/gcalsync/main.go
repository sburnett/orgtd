// Command gcalsync is a one-shot CLI, separate from the main orgtd
// binary, that syncs Google Calendar events into an org file
// (calendar.org, by default) inside orgtd's own org directory — plain
// headlines with a timestamp, browsable in orgtd's outline, but not
// SCHEDULED/DEADLINE so they don't show up in its agenda computation.
// The output file is wholesale-regenerated on every run: it's a cache,
// not something to hand-edit.
//
// Run it yourself on whatever schedule you like (cron, launchd,
// systemd timer); gcalsync doesn't loop or poll on its own.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gcal"
	"github.com/sburnett/orgtd/internal/org"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gcalsync: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "", "directory containing org files, shared with orgtd (default: $ORGTD_DIR, then the config file's org_dir, then ~/org)")
	outputFile := flag.String("output-file", "", "org file to regenerate with synced events, relative to -dir unless absolute (default: the config file's gcalsync.output_file, else calendar.org)")
	calendarIDs := flag.String("calendar-ids", "", "comma-separated Google Calendar IDs to sync (default: the config file's gcalsync.calendar_ids, else \"primary\")")
	syncPastDays := flag.Int("sync-past-days", 0, "how many days into the past the sync window starts (default: the config file's gcalsync.sync_past_days, else 1)")
	syncFutureDays := flag.Int("sync-future-days", 0, "how many days into the future the sync window ends (default: the config file's gcalsync.sync_future_days, else 14)")
	oauthClientID := flag.String("oauth-client-id", "", "Google OAuth2 client ID (installed-app / Desktop app type) (default: the config file's gcalsync.oauth_client_id)")
	oauthClientSecret := flag.String("oauth-client-secret", "", "Google OAuth2 client secret (default: the config file's gcalsync.oauth_client_secret)")
	reauth := flag.Bool("reauth", false, "discard any cached OAuth token and run the consent flow again before syncing")
	configPath := flag.String("config", "", "path to the TOML config file shared with orgtd (default: "+config.DefaultPath()+")")
	flag.Parse()

	explicit := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	s := resolveSettings(flagValues{
		dir:               *dir,
		outputFile:        *outputFile,
		calendarIDs:       splitList(*calendarIDs),
		syncPastDays:      *syncPastDays,
		syncFutureDays:    *syncFutureDays,
		oauthClientID:     *oauthClientID,
		oauthClientSecret: *oauthClientSecret,
		reauth:            *reauth,
		explicit:          explicit,
	}, os.Getenv("ORGTD_DIR"), cfg)

	if s.reauth {
		if err := gcal.ForgetToken(); err != nil {
			return err
		}
	}

	ctx := context.Background()
	httpClient, err := gcal.HTTPClient(ctx, s.oauthClientID, s.oauthClientSecret, func(url string) {
		fmt.Fprintf(os.Stderr, "gcalsync: opening browser for Google sign-in; if it doesn't open, visit:\n%s\n", url)
	})
	if err != nil {
		return err
	}
	client, err := gcal.NewClient(ctx, httpClient)
	if err != nil {
		return err
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	timeMin := startOfToday.AddDate(0, 0, -s.syncPastDays)
	timeMax := startOfToday.AddDate(0, 0, s.syncFutureDays+1)

	var events []gcal.Event
	for _, calID := range s.calendarIDs {
		evs, err := client.ListEvents(ctx, calID, timeMin, timeMax)
		if err != nil {
			return err
		}
		events = append(events, evs...)
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("gcalsync: creating org directory %s: %w", s.dir, err)
	}
	if err := org.WriteFile(BuildFile(s.outputFile, events)); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "gcalsync: synced %d event(s) from %d calendar(s) to %s\n",
		len(events), len(s.calendarIDs), s.outputFile)
	return nil
}
