package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"sbctl/internal/ui"
)

// ipInfoURL is the lookup service. It reports the address as seen from the
// public internet, which is what makes it useful for confirming that traffic is
// actually leaving through the tunnel.
const ipInfoURL = "https://ipinfo.io/json"

// ipInfo is the subset of the lookup response sbctl displays.
type ipInfo struct {
	IP       string `json:"ip"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Country  string `json:"country"`
	Loc      string `json:"loc"`
	Org      string `json:"org"`
	Postal   string `json:"postal"`
	Timezone string `json:"timezone"`
}

// fetcher retrieves a URL. Injected so the command is testable offline.
type fetcher func(ctx context.Context, url string) ([]byte, error)

func (a *App) ipCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ip",
		Short:   "Show the public IP address traffic is leaving from",
		GroupID: groupDiag,
		Long: "Look up the public IP address, network operator and approximate location as seen\n" +
			"from the internet.\n\n" +
			"This is the quickest way to confirm a profile is actually carrying traffic: run it\n" +
			"before and after `sbctl use` and compare.",
		Example: "  sbctl ip\n  sbctl ip --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.ipAction(cmd.Context(), httpFetch)
		},
	}
}

func (a *App) ipAction(ctx context.Context, fetch fetcher) error {
	body, err := fetch(ctx, ipInfoURL)
	if err != nil {
		return (&Error{Code: ExitError, Message: err.Error(), Err: err}).
			withHints(
				"check that you have a working connection",
				"if sing-box is running, inspect it with: sbctl logs",
			)
	}

	var info ipInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return failf("the lookup service returned something unreadable").wrap(err)
	}

	if a.Format.JSON {
		return a.emitJSON(map[string]any{
			"ip":       info.IP,
			"city":     info.City,
			"region":   info.Region,
			"country":  info.Country,
			"location": info.Loc,
			"network":  info.Org,
			"postal":   info.Postal,
			"timezone": info.Timezone,
		})
	}

	// Rendered through the same aligned key/value component as status and
	// doctor. The previous emoji-prefixed layout used variable-width glyphs, so
	// its columns never actually lined up.
	rows := []ui.KV{
		{Label: "ip", Value: info.IP},
		{Label: "network", Value: info.Org},
		{Label: "location", Value: joinNonEmpty(", ", info.City, info.Region, info.Country)},
		{Label: "coordinates", Value: info.Loc},
		{Label: "postal", Value: info.Postal},
		{Label: "timezone", Value: info.Timezone},
	}
	a.printf("%s", a.Theme.KVBlock(rows))
	return nil
}

// httpFetch performs the real lookup.
func httpFetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the IP lookup service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the IP lookup service returned %s", resp.Status)
	}
	// Bound the read so a misbehaving endpoint cannot exhaust memory.
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}
