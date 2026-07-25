package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// App bindings for user-to-user sharing. Regenerated into the frontend by
// build.bat (wails3 generate bindings).

// ShareIdentityUI is this client's own share identity, for display in the
// share dialog ("Your share ID: Name#A7K2XN").
type ShareIdentityUI struct {
	Addr       string `json:"addr"`
	Name       string `json:"name"`
	Registered bool   `json:"registered"`
	Linked     bool   `json:"linked"`
}

// ShareContactUI is one row of the recipient directory. Discord is the owner's
// Discord display name (handle fallback), empty for unlinked clients.
type ShareContactUI struct {
	Addr    string `json:"addr"`
	Name    string `json:"name"`
	Linked  bool   `json:"linked"`
	Discord string `json:"discord"`
}

// GetShareIdentity registers if needed and returns this client's identity.
func (a *App) GetShareIdentity() ShareIdentityUI {
	_ = ensureShareRegistered()
	s := GetSettings()
	return ShareIdentityUI{
		Addr:       s.ShareAddr,
		Name:       s.ShareName,
		Registered: s.ShareAddr != "",
		Linked:     IsLinked(),
	}
}

// GetShareDirectory returns the recently-seen clients a share can be sent to.
func (a *App) GetShareDirectory() ([]ShareContactUI, error) {
	if err := ensureShareRegistered(); err != nil {
		return nil, fmt.Errorf("sharing is unavailable — could not reach the server")
	}
	var out struct {
		Clients []ShareContactUI `json:"clients"`
	}
	if err := shareRequest(http.MethodGet, "/share/directory", nil, &out); err != nil {
		return nil, err
	}
	if out.Clients == nil {
		out.Clients = []ShareContactUI{}
	}
	return out.Clients, nil
}

// ShareTrigger sends one trigger (by session id) to another client. Only
// single triggers are shareable — the UI offers no share action on groups, and
// the server independently rejects any payload that isn't exactly one trigger,
// so the Fuse package can't travel this path even from a modified client.
func (a *App) ShareTrigger(triggerID int, toAddr string) error {
	if err := ensureShareRegistered(); err != nil {
		return fmt.Errorf("sharing is unavailable — could not reach the server")
	}
	trigStoreMu.Lock()
	t, ok := trigByID[triggerID]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	// Wrap the single trigger in a throwaway group so the payload round-trips
	// through the same <TriggerGroup> XML shape the rest of the app speaks.
	// The session ID field is xml:"-" and MediaFileName is already a bare
	// basename, so nothing machine-local leaks.
	body, err := marshalGroupXML(&GinaGroup{Name: "Shared Trigger", Triggers: []*GinaTrigger{t}})
	trigStoreMu.Unlock()
	if err != nil {
		return err
	}
	return shareRequest(http.MethodPost, "/share/send", map[string]string{
		"to_addr": toAddr, "kind": "trigger", "payload": string(body),
	}, nil)
}

// ShareMarkers sends a set of map markers for one zone. markersJSON is the
// frontend's [{name,x,y,z}] selection (markers live only in frontend
// localStorage — the Go side never stores them).
func (a *App) ShareMarkers(toAddr, zone, markersJSON string) error {
	if err := ensureShareRegistered(); err != nil {
		return fmt.Errorf("sharing is unavailable — could not reach the server")
	}
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return fmt.Errorf("no zone")
	}
	var markers []struct {
		Name string  `json:"name"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		Z    float64 `json:"z"`
	}
	if json.Unmarshal([]byte(markersJSON), &markers) != nil || len(markers) == 0 {
		return fmt.Errorf("no markers selected")
	}
	payload, _ := json.Marshal(map[string]any{"zone": zone, "markers": markers})
	return shareRequest(http.MethodPost, "/share/send", map[string]string{
		"to_addr": toAddr, "kind": "markers", "payload": string(payload),
	}, nil)
}

// GetShareInbox returns the cached pending shares and refreshes in the
// background so an open inbox stays current between 60s polls.
func (a *App) GetShareInbox() []ShareInboxItem {
	go func() {
		if ensureShareRegistered() == nil {
			if items, err := fetchShareInbox(); err == nil {
				setShareInbox(items)
			}
		}
	}()
	shareMu.Lock()
	defer shareMu.Unlock()
	out := make([]ShareInboxItem, len(shareInbox))
	copy(out, shareInbox)
	return out
}

// sharedGroupName is the Personal child group accepted triggers land in.
const sharedGroupName = "Shared"

// AcceptTriggerShare imports a pending trigger share into Personal > Shared,
// then acks the server. Re-accepting the same share replaces the same-named
// trigger instead of duplicating it, so a failed ack is harmless.
func (a *App) AcceptTriggerShare(id int) error {
	item, ok := cachedShareItem(id)
	if !ok {
		return fmt.Errorf("share no longer available")
	}
	if item.Kind != "trigger" {
		return fmt.Errorf("not a trigger share")
	}
	var g GinaGroup
	if xml.Unmarshal([]byte(item.Payload), &g) != nil {
		return fmt.Errorf("the shared trigger could not be read")
	}
	if len(g.Triggers) != 1 || len(g.Groups) != 0 {
		return fmt.Errorf("the share is not a single trigger")
	}
	t := g.Triggers[0]
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("the shared trigger has no name")
	}

	trigStoreMu.Lock()
	if personalRoot == nil {
		personalRoot = newPersonalRoot()
	}
	var shared *GinaGroup
	for _, c := range personalRoot.Groups {
		if strings.EqualFold(c.Name, sharedGroupName) {
			shared = c
			break
		}
	}
	if shared == nil {
		shared = &GinaGroup{Name: sharedGroupName, GroupID: nextGroupIDLocked()}
		personalRoot.Groups = append(personalRoot.Groups, shared)
	}
	replaced := false
	for i, existing := range shared.Triggers {
		if strings.EqualFold(existing.Name, t.Name) {
			shared.Triggers[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		shared.Triggers = append(shared.Triggers, t)
	}
	assembleLocked()                  // rebuild trigCfg + session ids
	localizeAndNormalizeMediaLocked() // scrub any path back to a bare media name
	err := saveTriggersLocked()
	trigStoreMu.Unlock()
	if err != nil {
		return err
	}
	RebuildTriggerActivation()

	go func() { _ = shareResolveRemote(id, "accept") }()
	removeShareFromCache(id)
	addStatus("Sharing: accepted trigger %q from %s", t.Name, item.FromName)
	return nil
}

// ResolveShare acks a share without a Go-side import: declines of any kind,
// and marker accepts (the frontend applies those to its own localStorage
// before calling this).
func (a *App) ResolveShare(id int, accept bool) error {
	action := "decline"
	if accept {
		action = "accept"
	}
	go func() { _ = shareResolveRemote(id, action) }()
	removeShareFromCache(id)
	return nil
}
