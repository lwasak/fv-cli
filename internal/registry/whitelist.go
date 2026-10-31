package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lwasak/fv-cli/internal/httpx"
)

type wlSubject struct {
	Name             string   `json:"name"`
	NIP              string   `json:"nip"`
	StatusVat        string   `json:"statusVat"`
	ResidenceAddress string   `json:"residenceAddress"`
	WorkingAddress   string   `json:"workingAddress"`
	AccountNumbers   []string `json:"accountNumbers"`
}

type wlResponse struct {
	Result struct {
		Subject   *wlSubject `json:"subject"`
		RequestID string     `json:"requestId"`
	} `json:"result"`
}

type wlError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// fetchWhiteList queries the White List for nip on date (YYYY-MM-DD).
func (r *Resolver) fetchWhiteList(ctx context.Context, nip, date string) (CacheEntry, error) {
	u := r.whiteListURL + "/api/search/nip/" + nip + "?date=" + date
	status, body, err := httpx.GetJSON(ctx, r.client, u, nil)
	if err != nil {
		if ctx.Err() != nil {
			return CacheEntry{}, ctx.Err()
		}
		return CacheEntry{}, fmt.Errorf("White List: %w: %v", errUnavailable, err)
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		return CacheEntry{}, fmt.Errorf("White List: %w: HTTP %d", errUnavailable, status)
	case status != http.StatusOK:
		var we wlError
		_ = json.Unmarshal(body, &we)
		msg := strings.TrimSpace(we.Code + " " + we.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", status)
		}
		low := strings.ToLower(we.Message)
		if strings.Contains(low, "limit") || strings.Contains(low, "przekrocz") {
			return CacheEntry{}, fmt.Errorf("White List: %w: %s", errUnavailable, msg)
		}
		return CacheEntry{}, fmt.Errorf("White List refused the request: %s", msg)
	}
	var resp wlResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return CacheEntry{}, fmt.Errorf("White List: %w: unreadable response: %v", errUnavailable, err)
	}
	sub := resp.Result.Subject
	if sub == nil {
		return CacheEntry{}, &notRegisteredError{
			msg:       fmt.Sprintf("NIP %s is %s in the White List on %s (status Niezarejestrowany)", nip, ErrNotRegistered, date),
			requestID: clean(resp.Result.RequestID),
		}
	}
	raw, err := stripPESEL(body)
	if err != nil {
		return CacheEntry{}, fmt.Errorf("White List: %w: %v", errUnavailable, err)
	}
	addr := cleanAddress(sub.WorkingAddress)
	if addr == "" {
		addr = cleanAddress(sub.ResidenceAddress)
	}
	var accts []string
	for _, a := range sub.AccountNumbers {
		if n := NormalizeAccount(a); n != "" {
			accts = append(accts, n)
		}
	}
	now := r.now()
	return CacheEntry{
		Key:    CacheKey(nip, ""),
		Source: SourceWhiteList,
		Raw:    raw,
		Party: Party{
			NIP: nip, CountryCode: "PL",
			Name: clean(sub.Name), Address: addr, StatusVat: clean(sub.StatusVat), AccountNumbers: accts,
		},
		RequestID: clean(resp.Result.RequestID),
		FetchedAt: now,
		QueryDate: date,
	}, nil
}

// stripPESEL re-marshals the response without result.subject.pesel, so personal data of
// sole traders is not stored unnecessarily.
func stripPESEL(body []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if res, ok := m["result"].(map[string]any); ok {
		if sub, ok := res["subject"].(map[string]any); ok {
			delete(sub, "pesel")
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, errors.New("re-encode White List response")
	}
	return out, nil
}
