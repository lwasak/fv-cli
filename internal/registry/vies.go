package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lwasak/fv-cli/internal/httpx"
)

type viesRequest struct {
	CountryCode              string `json:"countryCode"`
	VatNumber                string `json:"vatNumber"`
	RequesterMemberStateCode string `json:"requesterMemberStateCode,omitempty"`
	RequesterNumber          string `json:"requesterNumber,omitempty"`
}

type viesResponse struct {
	Valid             bool   `json:"valid"`
	RequestIdentifier string `json:"requestIdentifier"`
	Name              string `json:"name"`
	Address           string `json:"address"`
	ActionSucceed     *bool  `json:"actionSucceed"`
	ErrorWrappers     []struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	} `json:"errorWrappers"`
}

var viesUnavailable = map[string]bool{
	"MS_UNAVAILABLE": true, "MS_MAX_CONCURRENT_REQ": true, "TIMEOUT": true,
	"SERVICE_UNAVAILABLE": true, "GLOBAL_MAX_CONCURRENT_REQ": true,
}

// SplitVatID splits an EU VAT id into its VIES prefix and number and maps the prefix to an
// ISO country code (EL to GR, XI to GB). GR is refused with a hint, because VIES needs EL.
func SplitVatID(vatID string) (euCode, number, country string, err error) {
	v := strings.ToUpper(strings.ReplaceAll(vatID, " ", ""))
	if len(v) < 3 {
		return "", "", "", fmt.Errorf("VAT id %q is too short", vatID)
	}
	euCode, number = v[:2], v[2:]
	switch euCode {
	case "GR":
		return "", "", "", fmt.Errorf("VAT id %q: VIES uses the prefix EL for Greece, not GR", vatID)
	case "EL":
		country = "GR"
	case "XI":
		country = "GB"
	default:
		country = euCode
	}
	return euCode, number, country, nil
}

func missing(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.Trim(s, "-") == ""
}

// fetchVIES checks vatID in VIES with the seller as requester.
func (r *Resolver) fetchVIES(ctx context.Context, vatID string) (CacheEntry, error) {
	eu, num, country, err := SplitVatID(vatID)
	if err != nil {
		return CacheEntry{}, err
	}
	req := viesRequest{CountryCode: eu, VatNumber: num}
	if r.sellerNIP != "" {
		req.RequesterMemberStateCode, req.RequesterNumber = "PL", r.sellerNIP
	}
	status, body, err := httpx.PostJSON(ctx, r.viesClient, r.viesURL+"/check-vat-number", req, nil)
	if err != nil {
		if ctx.Err() != nil {
			return CacheEntry{}, ctx.Err()
		}
		return CacheEntry{}, fmt.Errorf("VIES: %w: %v", errUnavailable, err)
	}
	var resp viesResponse
	parseErr := json.Unmarshal(body, &resp)
	if len(resp.ErrorWrappers) > 0 {
		e := resp.ErrorWrappers[0]
		if viesUnavailable[e.Error] {
			return CacheEntry{}, fmt.Errorf("VIES: %w: %s %s", errUnavailable, e.Error, e.Message)
		}
		return CacheEntry{}, fmt.Errorf("VIES refused the request: %s %s", e.Error, e.Message)
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		return CacheEntry{}, fmt.Errorf("VIES: %w: HTTP %d", errUnavailable, status)
	case status != http.StatusOK:
		return CacheEntry{}, fmt.Errorf("VIES refused the request: HTTP %d", status)
	case parseErr != nil:
		return CacheEntry{}, fmt.Errorf("VIES: %w: unreadable response: %v", errUnavailable, parseErr)
	case !resp.Valid:
		return CacheEntry{}, fmt.Errorf("VAT number %s is %w: not valid in VIES", strings.ToUpper(vatID), ErrNotRegistered)
	}
	p := Party{
		VatID: eu + num, EUCode: eu, VatNumber: num, CountryCode: country,
	}
	if !missing(resp.Name) {
		p.Name = clean(resp.Name)
	}
	if !missing(resp.Address) {
		p.Address = cleanAddress(resp.Address)
	}
	return CacheEntry{
		Key:    CacheKey("", eu+num),
		Source: SourceVIES,
		Raw:    json.RawMessage(body),
		Party:  p, ConsultationNumber: clean(resp.RequestIdentifier),
		FetchedAt: r.now(),
	}, nil
}
