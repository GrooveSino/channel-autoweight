package fetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	m "autoweight/model"
)

// ErrAggregateOnly: plugin contract v1 logs.read returns aggregated buckets
// only (GET /logs/aggregate), never single rows. The ported core needs
// row-level data (10m windows, error bursts, matched ratios, per-token
// filters), so a plugin deployment must feed it an aggregate-based feature
// builder. Until that exists, Plugin can serve Channels/Disabled only.
var ErrAggregateOnly = errors.New("plugin logs.read is aggregate-only; row-level Logs unsupported")

// Plugin is the contract-v1 adapter (channels.read). Token is read by the
// caller from the environment and is never logged.
type Plugin struct {
	Base   string
	Token  string
	Client *http.Client
}

type pluginChan struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Status   int    `json:"status"`
	Weight   int    `json:"weight"`
	Priority int    `json:"priority"`
}

func (p *Plugin) channels(disabled bool) ([]m.Channel, error) {
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/channels?include_disabled=%v", p.Base, disabled), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin channels: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("plugin channels: http %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channels []pluginChan `json:"channels"`
		} `json:"data"`
		Channels []pluginChan `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	cs := body.Channels
	if len(cs) == 0 {
		cs = body.Data.Channels
	}
	out := []m.Channel{}
	for _, x := range cs {
		if (x.Status == 1) == disabled {
			continue
		}
		out = append(out, m.Channel{ID: x.ID, Name: x.Name, Weight: x.Weight, Priority: x.Priority, Status: x.Status})
	}
	return out, nil
}

func (p *Plugin) Channels() ([]m.Channel, error) { return p.channels(false) }
func (p *Plugin) Disabled() ([]m.Channel, error) { return p.channels(true) }
func (p *Plugin) Logs([]int, int) (int64, []m.Row, error) {
	return 0, nil, ErrAggregateOnly
}
