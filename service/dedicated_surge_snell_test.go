package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/util"

	"gorm.io/gorm"
)

func createDedicatedSnellInbound(t *testing.T, db *gorm.DB, tag string) *model.Inbound {
	t.Helper()
	inbound := &model.Inbound{
		Type:    "snell",
		Tag:     tag,
		Options: json.RawMessage(`{"listen":"::","listen_port":30660,"version":5,"psk":"test-psk","obfs_mode":"none"}`),
		Addrs:   json.RawMessage(`[]`),
		OutJson: json.RawMessage(`null`),
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("creating inbound: %v", err)
	}
	return inbound
}

func bindClientToInbound(t *testing.T, db *gorm.DB, client *model.Client, inboundIDs ...uint) {
	t.Helper()
	raw, err := json.Marshal(inboundIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(client).Update("inbounds", raw).Error; err != nil {
		t.Fatal(err)
	}
	client.Inbounds = raw
}

func TestDedicatedSurgeSnellMarker(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		tag      string
		want     bool
	}{
		{"snell", "SurgeSnell-Alice", true},
		{"snell", "surgesnell-Alice", false},
		{"snell", "Snell-Alice", false},
		{"vless", "SurgeSnell-Alice", false},
	} {
		if got := util.IsSurgeSnellInbound(tc.protocol, tc.tag); got != tc.want {
			t.Errorf("IsSurgeSnellInbound(%q, %q) = %v, want %v", tc.protocol, tc.tag, got, tc.want)
		}
	}
}

func TestParseDedicatedSurgeSnellClientIDs(t *testing.T) {
	for _, value := range []string{"2", " 2 ", "2,2", ",2,"} {
		ids, err := parseDedicatedSurgeSnellClientIDs(value, true)
		if err != nil || len(ids) != 1 || ids[0] != 2 {
			t.Errorf("%q should resolve to client 2, got %v, %v", value, ids, err)
		}
	}
	for _, value := range []string{"", "2,3", "0", "not-an-id"} {
		if _, err := parseDedicatedSurgeSnellClientIDs(value, true); err == nil {
			t.Errorf("%q should be rejected", value)
		}
	}
}

func TestDedicatedSurgeSnellClientActive(t *testing.T) {
	const now = int64(1_000)
	for name, tc := range map[string]struct {
		client *dedicatedSurgeSnellClient
		want   bool
	}{
		"nil":             {nil, false},
		"disabled":        {&dedicatedSurgeSnellClient{}, false},
		"unlimited":       {&dedicatedSurgeSnellClient{Enable: true}, true},
		"at quota":        {&dedicatedSurgeSnellClient{Enable: true, Volume: 100, Up: 40, Down: 60}, true},
		"over quota":      {&dedicatedSurgeSnellClient{Enable: true, Volume: 100, Up: 41, Down: 60}, false},
		"expires now":     {&dedicatedSurgeSnellClient{Enable: true, Expiry: now}, true},
		"already expired": {&dedicatedSurgeSnellClient{Enable: true, Expiry: now - 1}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.client.active(now); got != tc.want {
				t.Errorf("active = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDedicatedSurgeSnellConfigIsPSKOnlyAndFailClosed(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Alice")
	client := createClient(t, db, &model.Client{Name: "Alice", Enable: true})
	bindClientToInbound(t, db, client, inbound.Id)

	configs, err := (&InboundService{}).GetAllConfig(db)
	if err != nil {
		t.Fatalf("GetAllConfig: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected one active config, got %d", len(configs))
	}
	var config map[string]interface{}
	if err = json.Unmarshal(configs[0], &config); err != nil {
		t.Fatal(err)
	}
	if _, exists := config["users"]; exists {
		t.Fatalf("dedicated Snell must stay PSK-only, got %s", configs[0])
	}
	if config["psk"] != "test-psk" {
		t.Errorf("PSK was not preserved: %v", config["psk"])
	}

	if err = db.Model(client).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	configs, err = (&InboundService{}).GetAllConfig(db)
	if err != nil {
		t.Fatalf("GetAllConfig for disabled client: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("disabled client's listener must be absent, got %d configs", len(configs))
	}

	if err = db.Model(client).Updates(map[string]interface{}{
		"enable": true,
		"expiry": time.Now().Unix() - 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	configs, err = (&InboundService{}).GetAllConfig(db)
	if err != nil {
		t.Fatalf("GetAllConfig for expired client: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expired client's listener must be absent, got %d configs", len(configs))
	}

	if err = db.Model(client).Updates(map[string]interface{}{
		"expiry": 0,
		"volume": 100,
		"up":     60,
		"down":   41,
	}).Error; err != nil {
		t.Fatal(err)
	}
	configs, err = (&InboundService{}).GetAllConfig(db)
	if err != nil {
		t.Fatalf("GetAllConfig for over-quota client: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("over-quota client's listener must be absent, got %d configs", len(configs))
	}
}

func TestDedicatedSurgeSnellUnassignedAndMultipleOwnersFailClosed(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Shared")

	configs, err := (&InboundService{}).GetAllConfig(db)
	if err != nil || len(configs) != 0 {
		t.Fatalf("unassigned listener should be omitted, configs=%d err=%v", len(configs), err)
	}

	bound, _ := json.Marshal([]uint{inbound.Id})
	createClient(t, db, &model.Client{Name: "Alice", Enable: true, Inbounds: bound})
	createClient(t, db, &model.Client{Name: "Bob", Enable: true, Inbounds: bound})
	if _, err = (&InboundService{}).GetAllConfig(db); err == nil {
		t.Fatal("multiple owners should fail closed")
	}
}

func TestDedicatedSurgeSnellAssignmentValidation(t *testing.T) {
	db := clientTestDB(t)
	first := createDedicatedSnellInbound(t, db, "SurgeSnell-First")
	second := createDedicatedSnellInbound(t, db, "SurgeSnell-Second")
	service := &ClientService{}

	twoIDs, _ := json.Marshal([]uint{first.Id, second.Id})
	if err := service.validateDedicatedSurgeSnellAssignments(db, []*model.Client{{
		Name: "Alice", Inbounds: twoIDs,
	}}); err == nil {
		t.Fatal("one client owning two dedicated inbounds should be rejected")
	}

	firstOnly, _ := json.Marshal([]uint{first.Id})
	if err := service.validateDedicatedSurgeSnellAssignments(db, []*model.Client{
		{Name: "Alice", Inbounds: firstOnly},
		{Name: "Bob", Inbounds: firstOnly},
	}); err == nil {
		t.Fatal("two proposed clients sharing one dedicated inbound should be rejected")
	}

	owner := createClient(t, db, &model.Client{Name: "Owner", Enable: true, Inbounds: firstOnly})
	if err := service.validateDedicatedSurgeSnellAssignments(db, []*model.Client{{
		Name: "Intruder", Inbounds: firstOnly,
	}}); err == nil {
		t.Fatal("claiming another client's inbound should be rejected")
	}
	if err := service.validateDedicatedSurgeSnellAssignments(db, []*model.Client{{
		Id: owner.Id, Name: owner.Name, Inbounds: firstOnly,
	}}); err != nil {
		t.Fatalf("the persisted owner should be accepted: %v", err)
	}
}

func TestDedicatedSurgeSnellInboundCreationRequiresFreeOwner(t *testing.T) {
	db := clientTestDB(t)
	service := &InboundService{}
	free := createClient(t, db, &model.Client{Name: "Free", Enable: true})

	if err := service.validateNewDedicatedSurgeSnellOwner(db, ""); err == nil {
		t.Fatal("an owner is required")
	}
	if err := service.validateNewDedicatedSurgeSnellOwner(db, "999999"); err == nil {
		t.Fatal("the owner must exist")
	}
	if err := service.validateNewDedicatedSurgeSnellOwner(db, strconv.FormatUint(uint64(free.Id), 10)); err != nil {
		t.Fatalf("free owner rejected: %v", err)
	}

	existing := createDedicatedSnellInbound(t, db, "SurgeSnell-Existing")
	bindClientToInbound(t, db, free, existing.Id)
	if err := service.validateNewDedicatedSurgeSnellOwner(db, strconv.FormatUint(uint64(free.Id), 10)); err == nil {
		t.Fatal("a client already owning a dedicated inbound should be rejected")
	}
}

func TestDedicatedSurgeSnellLinksRefreshAfterInboundChange(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Alice")
	client := createClient(t, db, &model.Client{Name: "Alice", Enable: true, Remark: "VIP"})
	service := &ClientService{}

	if err := service.UpdateClientsOnInboundAdd(
		db, strconv.FormatUint(uint64(client.Id), 10), inbound.Id, "198.51.100.10",
	); err != nil {
		t.Fatalf("UpdateClientsOnInboundAdd: %v", err)
	}
	got := reload(t, db, client.Id)
	assertOnlyLocalLink(t, got.Links,
		"VIP-SurgeSnell-Alice = snell, 198.51.100.10, 30660, psk=test-psk, version=5, reuse=true")

	oldTag := inbound.Tag
	inbound.Tag = "SurgeSnell-Alice-New"
	inbound.Options = json.RawMessage(`{"listen":"::","listen_port":52728,"version":6,"psk":"rotated-psk","mode":"unshaped"}`)
	if err := db.Save(inbound).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateLinksByInboundChange(db, &[]model.Inbound{*inbound}, "203.0.113.7", oldTag); err != nil {
		t.Fatalf("UpdateLinksByInboundChange: %v", err)
	}
	got = reload(t, db, client.Id)
	assertOnlyLocalLink(t, got.Links,
		"VIP-SurgeSnell-Alice-New = snell, 203.0.113.7, 52728, psk=rotated-psk, version=6, reuse=true, mode=unshaped")
}

func assertOnlyLocalLink(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var links []map[string]string
	if err := json.Unmarshal(raw, &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0]["type"] != "local" || links[0]["uri"] != want {
		t.Fatalf("links = %s, want one local link %q", raw, want)
	}
}

func TestDedicatedSurgeSnellAutoResetReenablesListenerOwner(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Reset")
	client := createClient(t, db, &model.Client{
		Name:      "Reset",
		Enable:    false,
		AutoReset: true,
		ResetDays: 30,
		NextReset: 999,
		Up:        400,
		Down:      600,
	})
	bindClientToInbound(t, db, client, inbound.Id)

	ids, err := (&ClientService{}).ResetClients(db, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != inbound.Id {
		t.Fatalf("changed inbound ids = %v, want [%d]", ids, inbound.Id)
	}
	got := reload(t, db, client.Id)
	if !got.Enable || got.Up != 0 || got.Down != 0 {
		t.Fatalf("reset client = enable:%v up:%d down:%d", got.Enable, got.Up, got.Down)
	}
}

func TestAppendDedicatedSurgeSnellUserStats(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Alice")
	client := createClient(t, db, &model.Client{Name: "Alice", Enable: true})
	bindClientToInbound(t, db, client, inbound.Id)

	raw := []model.Stats{
		{Id: 99, Resource: "inbound", Tag: inbound.Tag, Direction: true, Traffic: 100},
		{Resource: "inbound", Tag: inbound.Tag, Direction: false, Traffic: 200},
		{Resource: "inbound", Tag: "ordinary", Direction: false, Traffic: 300},
		{Resource: "user", Tag: "existing", Direction: false, Traffic: 400},
	}
	got, err := appendDedicatedSurgeSnellUserStats(db, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(raw)+2 {
		t.Fatalf("got %d rows, want %d: %#v", len(got), len(raw)+2, got)
	}
	if len(raw) != 4 || raw[0].Resource != "inbound" || raw[0].Tag != inbound.Tag {
		t.Fatalf("raw counters were mutated: %#v", raw)
	}

	for i, wantTraffic := range []int64{100, 200} {
		stat := got[len(raw)+i]
		if stat.Id != 0 || stat.Resource != "user" || stat.Tag != "Alice" || stat.Traffic != wantTraffic {
			t.Errorf("synthetic row %d = %#v", i, stat)
		}
	}
}

func TestAppendDedicatedSurgeSnellUserStatsRejectsAmbiguousBinding(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "SurgeSnell-Shared")
	bound, _ := json.Marshal([]uint{inbound.Id})
	createClient(t, db, &model.Client{Name: "Alice", Enable: true, Inbounds: bound})
	createClient(t, db, &model.Client{Name: "Bob", Enable: true, Inbounds: bound})

	_, err := appendDedicatedSurgeSnellUserStats(db, []model.Stats{{
		Resource: "inbound",
		Tag:      inbound.Tag,
		Traffic:  1,
	}})
	if err == nil {
		t.Fatal("ambiguous binding should be rejected")
	}
}

func TestDedicatedSurgeSnellStatMarkerIsCaseSensitive(t *testing.T) {
	db := clientTestDB(t)
	inbound := createDedicatedSnellInbound(t, db, "surgesnell-lowercase")
	client := createClient(t, db, &model.Client{Name: "Alice", Enable: true})
	bindClientToInbound(t, db, client, inbound.Id)

	got, err := appendDedicatedSurgeSnellUserStats(db, []model.Stats{{
		Resource: "inbound", Tag: inbound.Tag, Traffic: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("lowercase marker generated synthetic user stats: %s", fmt.Sprint(got))
	}
}
