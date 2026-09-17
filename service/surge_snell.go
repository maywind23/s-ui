package service

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/util"
	"github.com/alireza0/s-ui/util/common"

	"gorm.io/gorm"
)

// A dedicated Surge Snell listener authenticates with one shared PSK. Surge
// cannot send sing-box's per-user userkey, so clients.inbounds is ownership and
// accounting metadata in this mode, never a Snell users array.
func isDedicatedSurgeSnellJSON(inboundType string, inboundJSON []byte) bool {
	if inboundType != "snell" {
		return false
	}
	var meta struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(inboundJSON, &meta); err != nil {
		return false
	}
	return util.IsSurgeSnellInbound(inboundType, meta.Tag)
}

type dedicatedSurgeSnellClient struct {
	ID     uint   `gorm:"column:id"`
	Name   string `gorm:"column:name"`
	Enable bool   `gorm:"column:enable"`
	Volume int64  `gorm:"column:volume"`
	Expiry int64  `gorm:"column:expiry"`
	Up     int64  `gorm:"column:up"`
	Down   int64  `gorm:"column:down"`
}

func (client *dedicatedSurgeSnellClient) active(now int64) bool {
	if client == nil || !client.Enable {
		return false
	}
	// Match ClientService.DepleteClients exactly. Keeping the listener closed
	// here also covers the short interval before the minute job persists the
	// disabled state.
	if client.Volume > 0 && client.Up+client.Down > client.Volume {
		return false
	}
	return client.Expiry <= 0 || client.Expiry >= now
}

// dedicatedSurgeSnellBinding resolves the sole Client assigned to a listener.
// Ambiguous legacy/corrupt data fails closed instead of charging an arbitrary
// account.
func (s *InboundService) dedicatedSurgeSnellBinding(db *gorm.DB, inboundID uint) (*dedicatedSurgeSnellClient, error) {
	var clients []dedicatedSurgeSnellClient
	err := db.Raw(
		`SELECT DISTINCT clients.id, clients.name, clients.enable, clients.volume,
		        clients.expiry, clients.up, clients.down
		 FROM clients, json_each(clients.inbounds) AS je
		 WHERE CAST(je.value AS INTEGER) = ?`,
		inboundID,
	).Scan(&clients).Error
	if err != nil {
		return nil, err
	}
	if len(clients) > 1 {
		return nil, common.NewErrorf(
			"dedicated Surge Snell inbound %d is assigned to %d clients; only one is allowed",
			inboundID, len(clients),
		)
	}
	if len(clients) == 0 {
		return nil, nil
	}
	return &clients[0], nil
}

func parseDedicatedSurgeSnellClientIDs(clientIDs string, requireOne bool) ([]uint, error) {
	unique := make(map[uint]struct{}, 1)
	for _, rawID := range strings.Split(clientIDs, ",") {
		rawID = strings.TrimSpace(rawID)
		if rawID == "" {
			continue
		}
		parsed, err := strconv.ParseUint(rawID, 10, 64)
		if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
			return nil, common.NewErrorf("invalid client id %q for dedicated Surge Snell", rawID)
		}
		unique[uint(parsed)] = struct{}{}
	}
	if len(unique) > 1 {
		return nil, common.NewError("dedicated Surge Snell can be assigned to only one client")
	}
	if requireOne && len(unique) != 1 {
		return nil, common.NewError("dedicated Surge Snell must be assigned to exactly one client")
	}
	ids := make([]uint, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	return ids, nil
}

func dedicatedSurgeSnellCountForClient(db *gorm.DB, clientID uint, exceptInboundID uint) (int64, error) {
	var count int64
	query := db.Raw(
		`SELECT COUNT(DISTINCT inbounds.id)
		 FROM clients, json_each(clients.inbounds) AS je, inbounds
		 WHERE clients.id = ?
		   AND inbounds.id = CAST(je.value AS INTEGER)
		   AND inbounds.type = 'snell'
		   AND substr(inbounds.tag, 1, ?) = ?
		   AND inbounds.id != ?`,
		clientID, len(util.SurgeSnellTagPrefix), util.SurgeSnellTagPrefix, exceptInboundID,
	)
	if err := query.Scan(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *InboundService) validateNewDedicatedSurgeSnellOwner(db *gorm.DB, clientIDs string) error {
	ids, err := parseDedicatedSurgeSnellClientIDs(clientIDs, true)
	if err != nil {
		return err
	}
	var count int64
	if err = db.Model(model.Client{}).Where("id = ?", ids[0]).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return common.NewErrorf("client %d does not exist", ids[0])
	}
	existing, err := dedicatedSurgeSnellCountForClient(db, ids[0], 0)
	if err != nil {
		return err
	}
	if existing > 0 {
		return common.NewErrorf("client %d already owns a dedicated Surge Snell inbound", ids[0])
	}
	return nil
}

func (s *InboundService) validateExistingDedicatedSurgeSnellOwner(db *gorm.DB, inboundID uint) error {
	client, err := s.dedicatedSurgeSnellBinding(db, inboundID)
	if err != nil {
		return err
	}
	if client == nil {
		return common.NewErrorf("dedicated Surge Snell inbound %d must be assigned to exactly one client", inboundID)
	}
	existing, err := dedicatedSurgeSnellCountForClient(db, client.ID, inboundID)
	if err != nil {
		return err
	}
	if existing > 0 {
		return common.NewErrorf("client %q already owns another dedicated Surge Snell inbound", client.Name)
	}
	return nil
}

type surgeSnellClaim struct {
	clientID uint
	name     string
	batchKey int
}

// validateDedicatedSurgeSnellAssignments enforces both halves of the 1:1
// relation when Clients are created or edited. Existing clients included in a
// bulk edit are evaluated by their proposed state, so an atomic reassignment
// remains possible.
func (s *ClientService) validateDedicatedSurgeSnellAssignments(db *gorm.DB, clients []*model.Client) error {
	clientInboundIDs := make([][]uint, len(clients))
	var allInboundIDs []uint
	proposedClientIDs := make(map[uint]struct{}, len(clients))
	for index, client := range clients {
		if err := json.Unmarshal(client.Inbounds, &clientInboundIDs[index]); err != nil {
			return err
		}
		allInboundIDs = common.UnionUintArray(allInboundIDs, clientInboundIDs[index])
		if client.Id != 0 {
			proposedClientIDs[client.Id] = struct{}{}
		}
	}
	if len(allInboundIDs) == 0 {
		return nil
	}

	var inbounds []model.Inbound
	if err := db.Model(model.Inbound{}).Where("id in ?", allInboundIDs).Find(&inbounds).Error; err != nil {
		return err
	}
	dedicatedIDs := make(map[uint]struct{})
	for _, inbound := range inbounds {
		if util.IsSurgeSnellInbound(inbound.Type, inbound.Tag) {
			dedicatedIDs[inbound.Id] = struct{}{}
		}
	}

	claims := make(map[uint]surgeSnellClaim, len(dedicatedIDs))
	for index, inboundIDs := range clientInboundIDs {
		seenForClient := make(map[uint]struct{}, 1)
		for _, inboundID := range inboundIDs {
			if _, dedicated := dedicatedIDs[inboundID]; dedicated {
				seenForClient[inboundID] = struct{}{}
			}
		}
		if len(seenForClient) > 1 {
			return common.NewErrorf("client %q can own only one dedicated Surge Snell inbound", clients[index].Name)
		}
		for inboundID := range seenForClient {
			claim := surgeSnellClaim{clientID: clients[index].Id, name: clients[index].Name, batchKey: index}
			if old, exists := claims[inboundID]; exists && old.batchKey != index {
				return common.NewErrorf(
					"dedicated Surge Snell inbound %d cannot be shared by clients %q and %q",
					inboundID, old.name, claim.name,
				)
			}
			claims[inboundID] = claim
		}
	}
	if len(claims) == 0 {
		return nil
	}

	selectedIDs := make([]uint, 0, len(claims))
	for inboundID := range claims {
		selectedIDs = append(selectedIDs, inboundID)
	}
	type persistedOwner struct {
		InboundID uint   `gorm:"column:inbound_id"`
		ClientID  uint   `gorm:"column:client_id"`
		Name      string `gorm:"column:name"`
	}
	var owners []persistedOwner
	if err := db.Raw(
		`SELECT DISTINCT CAST(je.value AS INTEGER) AS inbound_id,
		        clients.id AS client_id, clients.name AS name
		 FROM clients, json_each(clients.inbounds) AS je
		 WHERE CAST(je.value AS INTEGER) IN ?`,
		selectedIDs,
	).Scan(&owners).Error; err != nil {
		return err
	}
	for _, owner := range owners {
		claim := claims[owner.InboundID]
		if claim.clientID != 0 && claim.clientID == owner.ClientID {
			continue
		}
		if _, beingEdited := proposedClientIDs[owner.ClientID]; beingEdited {
			continue
		}
		return common.NewErrorf(
			"dedicated Surge Snell inbound %d is already owned by client %q",
			owner.InboundID, owner.Name,
		)
	}
	return nil
}
