package game

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"ships3d/i18n"
	"ships3d/models"
)

// Identity is who opened a connection: a logged-in user, or a guest when
// UserId is empty.
type Identity struct {
	UserId   string
	Username string
}

func (i Identity) Guest() bool { return i.UserId == "" }

// ShipLoader returns the design of the ship a player chose. It is given the
// player's identity so it can refuse a project that isn't theirs.
type ShipLoader func(identity Identity, shipId string) (*models.Project3d, error)

var ErrShipNotAllowed = errors.New("that ship is not available")

const (
	maxNameLength = 20
	maxBulletId   = 40
	// hitTolerance is how far, beyond a ship's radius, a reported hit may be
	// from where the server believes the target was. Positions reach the
	// server up to a round trip late, so an exact check would reject most
	// honest hits; this only rejects reports that are plainly impossible.
	hitTolerance = 30.0
	// latencyAllowance is extra bullet flight time granted when checking a
	// hit, for the same reason.
	latencyAllowance = 0.3
)

// Player is a connection that has joined the game. All fields are guarded by
// Hub.mu.
type Player struct {
	id       string
	client   *client
	identity Identity
	name     string
	ship     ShipInfo
	scale    float64

	pos, vel vec3
	rot      quat
	dirty    bool

	life     float64
	dead     bool
	diedAt   time.Time
	kills    int
	deaths   int
	lastFire time.Time
	// lastMissile is when they last fired a missile.
	lastMissile time.Time

	// Assault mode only.
	team    string
	hits    int
	seat    *Seat
	arrival int
}

func (p *Player) info() PlayerInfo {
	return PlayerInfo{
		Id: p.id, Name: p.name, Guest: p.identity.Guest(), Ship: p.ship, Scale: p.scale,
		Life: p.life, Dead: p.dead, Kills: p.kills, Deaths: p.deaths,
		P: p.pos, Q: p.rot, V: p.vel,
		Team: p.team, Hits: p.hits, Seat: p.seat,
	}
}

type bullet struct {
	owner   string
	origin  vec3
	dir     vec3
	firedAt time.Time
	// target is the player (or, in the assault, the mothership) a missile
	// homes on; empty for a bullet.
	target string
}

// Hub owns the state of the one running game. Positions are client
// authoritative (each client simulates its own ship and reports it); life,
// kills, deaths and respawns are server authoritative, and every reported hit
// is checked against the server's own record of the bullet and the target.
type Hub struct {
	settings    Settings
	loadShip    ShipLoader
	identify    func(*http.Request) Identity
	upgrader    websocket.Upgrader
	now         func() time.Time
	randomFloat func() float64

	mu      sync.Mutex
	players map[string]*Player
	bullets map[string]*bullet
}

func NewHub(settings Settings, loadShip ShipLoader, identify func(*http.Request) Identity, checkOrigin func(*http.Request) bool) *Hub {
	return &Hub{
		settings: settings,
		loadShip: loadShip,
		identify: identify,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// The connection is authenticated by the session cookie, which
			// the browser attaches whatever page opened the socket. Checking
			// the origin is what stops another site from opening one with
			// the player's cookie.
			CheckOrigin: checkOrigin,
		},
		now:         time.Now,
		randomFloat: rand.Float64,
		players:     map[string]*Player{},
		bullets:     map[string]*bullet{},
	}
}

// Run broadcasts snapshots until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second / time.Duration(h.settings.TickRate))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.tick()
		}
	}
}

// tick sends every moved player's state to everybody and forgets expired
// bullets.
func (h *Hub) tick() {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	for id, b := range h.bullets {
		if now.Sub(b.firedAt) > projectileTtl(h.settings, b) {
			delete(h.bullets, id)
		}
	}

	states := []playerState{}
	for _, p := range h.players {
		if p.dirty {
			states = append(states, playerState{Id: p.id, P: p.pos, Q: p.rot, V: p.vel})
			p.dirty = false
		}
	}
	if len(states) > 0 {
		h.broadcastLocked(snapshotMsg{Type: msgSnapshot, Players: states}, "")
	}
}

// ServeWS upgrades an HTTP request and runs the connection until it closes.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	identity := h.identify(r)
	serveConnection(&h.upgrader, h.now, w, r, func(id string, c *client, data []byte) {
		h.handleMessage(id, c, identity, data)
	}, h.leave)
}

func (h *Hub) handleMessage(id string, c *client, identity Identity, data []byte) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	switch env.Type {
	case msgJoin:
		var msg joinMsg
		if json.Unmarshal(data, &msg) == nil {
			h.join(id, c, identity, msg)
		}
	case msgState:
		var msg stateMsg
		if json.Unmarshal(data, &msg) == nil {
			h.updateState(id, msg)
		}
	case msgFire:
		var msg fireMsg
		if json.Unmarshal(data, &msg) == nil {
			h.fire(id, msg)
		}
	case msgMissile:
		var msg missileMsg
		if json.Unmarshal(data, &msg) == nil {
			h.launch(id, msg)
		}
	case msgHit:
		var msg hitMsg
		if json.Unmarshal(data, &msg) == nil {
			h.hit(id, msg)
		}
	case msgRespawn:
		h.respawn(id)
	case msgCrash:
		var msg crashMsg
		if json.Unmarshal(data, &msg) == nil {
			h.crash(id, msg)
		}
	}
}

func (h *Hub) join(id string, c *client, identity Identity, msg joinMsg) {
	h.mu.Lock()
	_, already := h.players[id]
	h.mu.Unlock()
	if already {
		return
	}

	// Loaded outside the lock: it may hit the database.
	project, err := h.loadShip(identity, msg.ShipId)
	if err != nil || project == nil {
		h.sendTo(c, errorMsg{Type: msgError, Message: i18n.New("shipNotAllowed").In(c.lang)})
		return
	}
	radius := project.BoundingRadius()
	if radius <= 0 {
		h.sendTo(c, errorMsg{Type: msgError, Message: i18n.New("shipNoVisibleParts").In(c.lang)})
		return
	}

	p := &Player{
		id:       id,
		client:   c,
		identity: identity,
		name:     playerName(identity, msg.Name, c.lang),
		ship:     ShipInfo{Id: msg.ShipId, Name: project.Name, Layers: visibleLayers(project)},
		scale:    h.settings.ShipRadius / radius,
		life:     h.settings.MaxLife,
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	p.pos, p.rot = h.spawnPoint()
	players := make([]PlayerInfo, 0, len(h.players)+1)
	players = append(players, p.info())
	for _, other := range h.players {
		players = append(players, other.info())
	}
	h.players[id] = p
	h.sendTo(c, welcomeMsg{Type: msgWelcome, Id: id, Settings: h.settings, Players: players})
	h.broadcastLocked(playerJoinedMsg{Type: msgPlayerJoined, Player: p.info()}, id)
	log.Printf("game: %s joined as %q (%d players)", id, p.name, len(h.players))
}

func (h *Hub) leave(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.players[id]; !ok {
		return
	}
	delete(h.players, id)
	h.broadcastLocked(playerLeftMsg{Type: msgPlayerLeft, Id: id}, "")
	log.Printf("game: %s left (%d players)", id, len(h.players))
}

func (h *Hub) updateState(id string, msg stateMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.players[id]
	if !ok || p.dead || !finiteVec(msg.P) || !finiteVec(msg.V) {
		return
	}
	rot, ok := normalizeQuat(msg.Q)
	if !ok {
		return
	}
	p.pos = clampLength(msg.P, h.settings.WorldRadius)
	p.vel = clampLength(msg.V, math.Max(h.settings.MaxSpeed, -h.settings.MinSpeed))
	p.rot = rot
	p.dirty = true
}

func (h *Hub) fire(id string, msg fireMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.players[id]
	if !ok || p.dead || msg.Id == "" || len(msg.Id) > maxBulletId {
		return
	}
	now := h.now()
	// 20% leeway: messages bunch up on the network, so two shots fired a
	// full cooldown apart can arrive closer together than that.
	cooldown := time.Duration(float64(h.settings.FireCooldownMs)*0.8) * time.Millisecond
	if now.Sub(p.lastFire) < cooldown {
		return
	}
	dir, ok := normalize(msg.D)
	if !ok || !finiteVec(msg.P) {
		return
	}
	// A bullet must leave from roughly where the shooter is: its last
	// reported position, plus however far it can have flown since.
	if length(sub(msg.P, p.pos)) > h.settings.ShipRadius*3+h.settings.MaxSpeed*0.5 {
		return
	}
	bulletId := id + ":" + msg.Id
	if _, exists := h.bullets[bulletId]; exists {
		return
	}
	p.lastFire = now
	h.bullets[bulletId] = &bullet{owner: id, origin: msg.P, dir: dir, firedAt: now}
	h.broadcastLocked(fireOutMsg{Type: msgFire, Id: bulletId, Owner: id, P: msg.P, D: dir}, id)
}

// launch fires a homing missile at the ship the shooter locked on to, if
// their missile has reloaded and the target is somebody they can have locked
// on to: alive, and in range.
func (h *Hub) launch(id string, msg missileMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.players[id]
	if !ok || p.dead || msg.Id == "" || len(msg.Id) > maxBulletId {
		return
	}
	target, ok := h.players[msg.Target]
	if !ok || target == p || target.dead || !inLockRange(h.settings, length(sub(target.pos, p.pos))) {
		return
	}
	now := h.now()
	dir, ok := normalize(msg.D)
	if !ok || !missileCooldownOver(h.settings, p, now) || !missileOriginOk(h.settings, p, msg.P) {
		return
	}
	missileId := id + ":" + msg.Id
	if _, exists := h.bullets[missileId]; exists {
		return
	}
	p.lastMissile = now
	h.bullets[missileId] = &bullet{owner: id, origin: msg.P, dir: dir, firedAt: now, target: target.id}
	h.broadcastLocked(missileOutMsg{Type: msgMissile, Id: missileId, Owner: id, Target: target.id, P: msg.P, D: dir}, id)
}

// hit applies a hit reported by the shooter, if it is plausible: the bullet
// must be the shooter's, still in flight, unused, and must have passed close
// to where the target was. A missile must have hit the ship it was fired at,
// and be within its reach (see missileReaches).
func (h *Hub) hit(id string, msg hitMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()

	bulletId := id + ":" + msg.BulletId
	b, ok := h.bullets[bulletId]
	if !ok {
		return
	}
	target, ok := h.players[msg.Target]
	if !ok || target.dead || target.id == id {
		return
	}
	elapsed := h.now().Sub(b.firedAt).Seconds()
	damage := h.settings.BulletDamage
	if b.missile() {
		if target.id != b.target || !missileReaches(h.settings, b, target.pos, elapsed) {
			return
		}
		damage = h.settings.MissileDamage
	} else {
		flight := math.Min(elapsed+latencyAllowance, float64(h.settings.BulletTtlMs)/1000)
		end := add(b.origin, mul(b.dir, flight*h.settings.BulletSpeed))
		if distanceToSegment(target.pos, b.origin, end) > h.settings.ShipRadius+hitTolerance {
			return
		}
	}

	delete(h.bullets, bulletId)
	target.life = math.Max(0, target.life-damage)
	h.broadcastLocked(damageMsg{Type: msgDamage, Target: target.id, From: id, BulletId: bulletId, Life: target.life}, "")
	if target.life > 0 {
		return
	}
	target.dead = true
	target.diedAt = h.now()
	target.deaths++
	if shooter, ok := h.players[id]; ok {
		shooter.kills++
	}
	h.broadcastLocked(diedMsg{Type: msgDied, Target: target.id, From: id}, "")
}

// crash destroys two ships that ran into each other, as the one reporting it
// saw it, if the server's own record of them agrees they were that close.
// Nobody gets a kill for it.
func (h *Hub) crash(id string, msg crashMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.players[id]
	other, otherOk := h.players[msg.With]
	if !ok || !otherOk || p == other || p.dead || other.dead {
		return
	}
	if !shipsTouch(p.pos, other.pos, h.settings.ShipRadius) {
		return
	}
	for _, ship := range []*Player{p, other} {
		ship.life = 0
		ship.dead = true
		ship.diedAt = h.now()
		ship.deaths++
		h.broadcastLocked(diedMsg{Type: msgDied, Target: ship.id}, "")
	}
}

// shipsTouch reports whether two ships, as the server last heard of them,
// may have collided: their hit spheres overlap, give or take how far
// positions can be behind (see hitTolerance).
func shipsTouch(a, b vec3, radius float64) bool {
	return length(sub(a, b)) <= 2*radius+hitTolerance
}

func (h *Hub) respawn(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.players[id]
	if !ok || !p.dead {
		return
	}
	if h.now().Sub(p.diedAt) < time.Duration(h.settings.RespawnDelayMs)*time.Millisecond {
		return
	}
	p.dead = false
	p.life = h.settings.MaxLife
	p.pos, p.rot = h.spawnPoint()
	p.vel = vec3{}
	h.broadcastLocked(respawnedMsg{Type: msgRespawned, Id: id, Life: p.life, P: p.pos, Q: p.rot}, "")
}

// spawnPoint picks a random position in the inner half of the world, facing
// a random direction on the horizontal plane.
func (h *Hub) spawnPoint() (vec3, quat) {
	r := h.settings.WorldRadius / 2
	pos := vec3{(h.randomFloat()*2 - 1) * r, (h.randomFloat()*2 - 1) * r / 4, (h.randomFloat()*2 - 1) * r}
	return pos, yawQuat(h.randomFloat() * 2 * math.Pi)
}

// broadcastLocked queues msg for every joined player except `except`. The
// caller holds mu; queueing never blocks, so that is safe.
func (h *Hub) broadcastLocked(msg any, except string) {
	frame, err := json.Marshal(msg)
	if err != nil {
		log.Println("game: marshal:", err)
		return
	}
	for id, p := range h.players {
		if id != except {
			p.client.enqueue(frame)
		}
	}
}

func (h *Hub) sendTo(c *client, msg any) { sendTo(c, msg) }

// PlayerCount is the number of players in game.
func (h *Hub) PlayerCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.players)
}

// playerName is the logged-in username, or a cleaned-up version of the name a
// guest typed. Guest names are free text from strangers, so control characters
// are dropped and the length capped; clients must still render it as text.
func playerName(identity Identity, requested string, lang i18n.Lang) string {
	if !identity.Guest() {
		return identity.Username
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(requested))
	if utf8.RuneCountInString(name) > maxNameLength {
		name = string([]rune(name)[:maxNameLength])
	}
	if name == "" {
		name = i18n.New("guest").In(lang)
	}
	return name
}

func visibleLayers(project *models.Project3d) []models.Layer3d {
	layers := []models.Layer3d{}
	for _, layer := range project.Layers {
		if layer.Visible && len(layer.Parts) > 0 {
			layers = append(layers, layer)
		}
	}
	return layers
}
