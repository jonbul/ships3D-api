package game

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"ships3d/i18n"
	"ships3d/models"
)

// Arena runs the assault mode: rooms of MinPlayers to MaxPlayers, each going
// lobby -> round -> results -> back to the lobby.
//
// There is always one open lobby, and everybody who connects joins it. It
// starts its round once it has enough players and nobody has joined for
// LobbyWaitMs (or at once when full), and a new, empty lobby opens for the
// next players. When a round ends, its players see the result for ResultsMs
// and then all go back to the open lobby together, as in the online mode of
// kart racing games.
//
// One mutex guards every room: rooms are small, and moving players between
// them needs no lock ordering this way. Timers are checked on the tick, with
// Arena.now, so tests can drive them with a fake clock.
type Arena struct {
	settings    Settings
	rules       AssaultRules
	loadShip    ShipLoader
	identify    func(*http.Request) Identity
	upgrader    websocket.Upgrader
	now         func() time.Time
	randomFloat func() float64
	shuffle     func(n int, swap func(i, j int))

	mothership      *models.Project3d
	mothershipScale float64
	hull            hull

	mu     sync.Mutex
	lobby  *room
	rounds map[*room]bool
	// rooms says which room each joined player is in.
	rooms map[string]*room
	// arrivals numbers players as they enter a lobby, to list them in order.
	arrivals int
}

type roomPhase int

const (
	phaseLobby roomPhase = iota
	phasePlaying
	phaseOver
)

type room struct {
	phase   roomPhase
	players map[string]*Player
	bullets map[string]*bullet
	// lastJoin starts the lobby's countdown; endsAt is when a round runs out
	// of time, or when its results end.
	lastJoin       time.Time
	endsAt         time.Time
	mothershipLife float64
}

func newRoom() *room {
	return &room{phase: phaseLobby, players: map[string]*Player{}, bullets: map[string]*bullet{}}
}

func NewArena(settings Settings, rules AssaultRules, loadShip ShipLoader, identify func(*http.Request) Identity, checkOrigin func(*http.Request) bool) *Arena {
	mothership := loadMothership()
	scale := rules.MothershipRadius / mothership.BoundingRadius()
	return &Arena{
		settings: settings,
		rules:    rules,
		loadShip: loadShip,
		identify: identify,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// See NewHub.
			CheckOrigin: checkOrigin,
		},
		now:             time.Now,
		randomFloat:     rand.Float64,
		shuffle:         rand.Shuffle,
		mothership:      mothership,
		mothershipScale: scale,
		hull:            newHull(mothership, scale),
		lobby:           newRoom(),
		rounds:          map[*room]bool{},
		rooms:           map[string]*room{},
	}
}

// roundSettings are the Settings in a round: the attackers' life and reload
// come from the rules, and a missile is one hit, like a bullet.
func (a *Arena) roundSettings() Settings {
	s := a.settings
	s.MaxLife = a.rules.AttackerLife
	s.FireCooldownMs = a.rules.AttackerCooldownMs
	s.MissileDamage = s.BulletDamage
	return s
}

// Run ticks every room until ctx is cancelled.
func (a *Arena) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second / time.Duration(a.settings.TickRate))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.tick()
		}
	}
}

// tick starts a lobby whose countdown is over, ends rounds that ran out of
// time, sends players back to the lobby after the results, and broadcasts
// moved ships.
func (a *Arena) tick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()

	if a.lobbyReady(now) {
		a.startRoundLocked()
	}
	for r := range a.rounds {
		switch r.phase {
		case phasePlaying:
			if !now.Before(r.endsAt) {
				// The mothership held out.
				a.endRoundLocked(r, TeamGunners)
				continue
			}
			for id, b := range r.bullets {
				if now.Sub(b.firedAt) > projectileTtl(a.settings, b) {
					delete(r.bullets, id)
				}
			}
			states := []playerState{}
			for _, p := range r.players {
				if p.dirty {
					states = append(states, playerState{Id: p.id, P: p.pos, Q: p.rot, V: p.vel})
					p.dirty = false
				}
			}
			if len(states) > 0 {
				broadcast(r, snapshotMsg{Type: msgSnapshot, Players: states}, "")
			}
		case phaseOver:
			if !now.Before(r.endsAt) {
				a.returnToLobbyLocked(r)
			}
		}
	}
}

func (a *Arena) lobbyReady(now time.Time) bool {
	n := len(a.lobby.players)
	return n >= a.rules.MinPlayers &&
		now.Sub(a.lobby.lastJoin) >= time.Duration(a.rules.LobbyWaitMs)*time.Millisecond
}

// ServeWS upgrades an HTTP request and runs the connection until it closes.
func (a *Arena) ServeWS(w http.ResponseWriter, r *http.Request) {
	identity := a.identify(r)
	serveConnection(&a.upgrader, a.now, w, r, func(id string, c *client, data []byte) {
		a.handleMessage(id, c, identity, data)
	}, a.leave)
}

func (a *Arena) handleMessage(id string, c *client, identity Identity, data []byte) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	switch env.Type {
	case msgJoin:
		var msg joinMsg
		if json.Unmarshal(data, &msg) == nil {
			a.join(id, c, identity, msg)
		}
	case msgState:
		var msg stateMsg
		if json.Unmarshal(data, &msg) == nil {
			a.updateState(id, msg)
		}
	case msgFire:
		var msg fireMsg
		if json.Unmarshal(data, &msg) == nil {
			a.fire(id, msg)
		}
	case msgMissile:
		var msg missileMsg
		if json.Unmarshal(data, &msg) == nil {
			a.launch(id, msg)
		}
	case msgDodge:
		var msg dodgeMsg
		if json.Unmarshal(data, &msg) == nil {
			a.dodge(id, msg)
		}
	case msgHit:
		var msg hitMsg
		if json.Unmarshal(data, &msg) == nil {
			a.hit(id, msg)
		}
	case msgRespawn:
		a.respawn(id)
	case msgCrash:
		var msg crashMsg
		if json.Unmarshal(data, &msg) == nil {
			a.crash(id, msg)
		}
	}
}

func (a *Arena) join(id string, c *client, identity Identity, msg joinMsg) {
	a.mu.Lock()
	_, already := a.rooms[id]
	a.mu.Unlock()
	if already {
		return
	}

	// Loaded outside the lock: it may hit the database.
	project, err := a.loadShip(identity, msg.ShipId)
	if err != nil || project == nil {
		sendTo(c, errorMsg{Type: msgError, Message: i18n.New("shipNotAllowed").In(c.lang)})
		return
	}
	radius := project.BoundingRadius()
	if radius <= 0 {
		sendTo(c, errorMsg{Type: msgError, Message: i18n.New("shipNoVisibleParts").In(c.lang)})
		return
	}
	p := &Player{
		id:       id,
		client:   c,
		identity: identity,
		name:     playerName(identity, msg.Name, c.lang),
		ship:     ShipInfo{Id: msg.ShipId, Name: project.Name, Layers: visibleLayers(project)},
		scale:    a.settings.ShipRadius / radius,
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, already := a.rooms[id]; already {
		return
	}
	a.admitLocked(p)
	a.broadcastLobbyLocked()
	log.Printf("assault: %s joined the lobby as %q (%d waiting)", id, p.name, len(a.lobby.players))
}

// admitLocked puts a player in the open lobby, restarting its countdown, and
// starts the lobby's round if that filled it.
func (a *Arena) admitLocked(p *Player) {
	a.arrivals++
	p.arrival = a.arrivals
	a.lobby.players[p.id] = p
	a.lobby.lastJoin = a.now()
	a.rooms[p.id] = a.lobby
	if len(a.lobby.players) >= a.rules.MaxPlayers {
		a.startRoundLocked()
	}
}

func (a *Arena) leave(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.rooms[id]
	if !ok {
		return
	}
	delete(a.rooms, id)
	p := r.players[id]
	delete(r.players, id)
	if r == a.lobby {
		a.broadcastLobbyLocked()
		return
	}
	broadcast(r, playerLeftMsg{Type: msgPlayerLeft, Id: id}, "")
	if len(r.players) == 0 {
		delete(a.rounds, r)
		return
	}
	if r.phase == phasePlaying {
		// A team that has nobody left loses.
		if teamSize(r, p.team) == 0 {
			winner := TeamGunners
			if p.team == TeamGunners {
				winner = TeamAttackers
			}
			a.endRoundLocked(r, winner)
		}
	}
}

func teamSize(r *room, team string) int {
	n := 0
	for _, p := range r.players {
		if p.team == team {
			n++
		}
	}
	return n
}

// startRoundLocked turns the open lobby into a round, and opens a new lobby.
func (a *Arena) startRoundLocked() {
	r := a.lobby
	a.lobby = newRoom()
	a.rounds[r] = true

	now := a.now()
	settings := a.roundSettings()
	r.phase = phasePlaying
	r.endsAt = now.Add(time.Duration(a.rules.RoundMs) * time.Millisecond)
	r.mothershipLife = a.rules.MothershipLife
	r.bullets = map[string]*bullet{}

	players := byArrival(r)
	a.shuffle(len(players), func(i, j int) { players[i], players[j] = players[j], players[i] })
	gunners := gunnerCount(len(players), a.rules.AttackersPerGunner)
	seats := seats(gunners, a.mothershipScale)
	for i, p := range players {
		p.life, p.dead, p.kills, p.deaths, p.hits = settings.MaxLife, false, 0, 0, 0
		p.vel, p.dirty, p.lastFire, p.lastMissile = vec3{}, false, time.Time{}, time.Time{}
		p.lastEvade = time.Time{}
		if i < gunners {
			seat := seats[i]
			p.team, p.seat = TeamGunners, &seat
			// Looking outwards, level.
			p.pos, p.rot = seat.P, yawQuat(math.Atan2(seat.P[0], seat.P[2]))
		} else {
			p.team, p.seat = TeamAttackers, nil
			p.pos, p.rot = a.attackerSpawn()
		}
	}

	infos := make([]PlayerInfo, len(players))
	for i, p := range players {
		infos[i] = p.info()
	}
	mothership := MothershipInfo{
		Ship:    ShipInfo{Id: "mothership", Name: a.mothership.Name, Layers: visibleLayers(a.mothership)},
		Scale:   a.mothershipScale,
		Life:    r.mothershipLife,
		MaxLife: a.rules.MothershipLife,
	}
	for _, p := range players {
		sendTo(p.client, roundStartMsg{
			Type: msgRoundStart, Id: p.id, Settings: settings, Rules: a.rules,
			Mothership: mothership, Players: infos, EndsInMs: int64(a.rules.RoundMs),
		})
	}
	log.Printf("assault: round started with %d players (%d gunners)", len(players), gunners)
}

// gunnerCount is one gunner per perGunner attackers, rounded, and at least one.
func gunnerCount(players, perGunner int) int {
	n := (players + (perGunner+1)/2) / (perGunner + 1)
	return max(1, min(n, players-1))
}

// attackerSpawn is a point SpawnDistance from the mothership, roughly level
// with it, facing it.
func (a *Arena) attackerSpawn() (vec3, quat) {
	angle := a.randomFloat() * 2 * math.Pi
	d := a.rules.SpawnDistance
	pos := vec3{math.Sin(angle) * d, (a.randomFloat()*2 - 1) * d / 6, math.Cos(angle) * d}
	return pos, yawQuat(angle + math.Pi)
}

func (a *Arena) endRoundLocked(r *room, winner string) {
	r.phase = phaseOver
	r.endsAt = a.now().Add(time.Duration(a.rules.ResultsMs) * time.Millisecond)
	r.bullets = map[string]*bullet{}
	broadcast(r, roundOverMsg{Type: msgRoundOver, Winner: winner, ReturnInMs: int64(a.rules.ResultsMs)}, "")
	log.Printf("assault: round over, %s win", winner)
}

// byArrival lists a room's players in the order they entered the lobby.
func byArrival(r *room) []*Player {
	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, p)
	}
	slices.SortFunc(players, func(a, b *Player) int { return a.arrival - b.arrival })
	return players
}

// returnToLobbyLocked moves everybody from a finished round to the open
// lobby, in the order they had arrived.
func (a *Arena) returnToLobbyLocked(r *room) {
	delete(a.rounds, r)
	for _, p := range byArrival(r) {
		p.team, p.seat, p.hits = "", nil, 0
		a.admitLocked(p)
	}
	a.broadcastLobbyLocked()
}

// broadcastLobbyLocked tells everybody in the open lobby who is there and
// when it starts.
func (a *Arena) broadcastLobbyLocked() {
	r := a.lobby
	players := make([]lobbyPlayer, 0, len(r.players))
	for _, p := range byArrival(r) {
		players = append(players, lobbyPlayer{Id: p.id, Name: p.name, Guest: p.identity.Guest()})
	}
	msg := lobbyMsg{Type: msgLobby, Players: players, MinPlayers: a.rules.MinPlayers, MaxPlayers: a.rules.MaxPlayers}
	if len(r.players) >= a.rules.MinPlayers {
		left := time.Duration(a.rules.LobbyWaitMs)*time.Millisecond - a.now().Sub(r.lastJoin)
		ms := max(0, left.Milliseconds())
		msg.StartsInMs = &ms
	}
	for _, p := range r.players {
		msg.Id = p.id
		sendTo(p.client, msg)
	}
}

// playing returns the player and their room if they are in a round that is
// still being played.
func (a *Arena) playing(id string) (*Player, *room) {
	r, ok := a.rooms[id]
	if !ok || r.phase != phasePlaying {
		return nil, nil
	}
	return r.players[id], r
}

func (a *Arena) updateState(id string, msg stateMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _ := a.playing(id)
	if p == nil || p.dead {
		return
	}
	rot, ok := normalizeQuat(msg.Q)
	if !ok {
		return
	}
	if p.team == TeamGunners {
		// A turret only turns.
		p.rot = rot
		p.dirty = true
		return
	}
	if !finiteVec(msg.P) || !finiteVec(msg.V) {
		return
	}
	p.pos = clampLength(msg.P, a.settings.WorldRadius)
	p.vel = clampLength(msg.V, math.Max(a.settings.MaxSpeed, -a.settings.MinSpeed))
	p.rot = rot
	p.dirty = true
}

func (a *Arena) fire(id string, msg fireMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, r := a.playing(id)
	if p == nil || p.dead || msg.Id == "" || len(msg.Id) > maxBulletId {
		return
	}
	now := a.now()
	cooldownMs := a.rules.AttackerCooldownMs
	if p.team == TeamGunners {
		cooldownMs = a.rules.GunnerCooldownMs
	}
	// 20% leeway, as in Hub.fire.
	if now.Sub(p.lastFire) < time.Duration(float64(cooldownMs)*0.8)*time.Millisecond {
		return
	}
	dir, ok := normalize(msg.D)
	if !ok || !finiteVec(msg.P) {
		return
	}
	if length(sub(msg.P, p.pos)) > a.settings.ShipRadius*3+a.settings.MaxSpeed*0.5 {
		return
	}
	bulletId := id + ":" + msg.Id
	if _, exists := r.bullets[bulletId]; exists {
		return
	}
	p.lastFire = now
	r.bullets[bulletId] = &bullet{owner: id, origin: msg.P, dir: dir, firedAt: now}
	broadcast(r, fireOutMsg{Type: msgFire, Id: bulletId, Owner: id, P: msg.P, D: dir}, id)
}

// launch fires a missile, as Hub.launch does, with teams: an attacker's
// homes on the mothership, a gunner's on an attacker. The mothership is in
// range when its hull is.
func (a *Arena) launch(id string, msg missileMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, r := a.playing(id)
	if p == nil || p.dead || msg.Id == "" || len(msg.Id) > maxBulletId {
		return
	}
	if msg.Target == mothershipTarget {
		if p.team != TeamAttackers || !a.hull.sphereHits(p.pos, a.settings.LockRange+hitTolerance+a.settings.MaxSpeed) {
			return
		}
	} else {
		target, ok := r.players[msg.Target]
		if !ok || target.dead || p.team != TeamGunners || target.team != TeamAttackers ||
			!inLockRange(a.settings, length(sub(target.pos, p.pos))) {
			return
		}
	}
	now := a.now()
	dir, ok := normalize(msg.D)
	if !ok || !missileCooldownOver(a.settings, p, now) || !missileOriginOk(a.settings, p, msg.P) {
		return
	}
	missileId := id + ":" + msg.Id
	if _, exists := r.bullets[missileId]; exists {
		return
	}
	p.lastMissile = now
	r.bullets[missileId] = &bullet{owner: id, origin: msg.P, dir: dir, firedAt: now, target: msg.Target}
	broadcast(r, missileOutMsg{Type: msgMissile, Id: missileId, Owner: id, Target: msg.Target, P: msg.P, D: dir}, id)
}

// dodge shakes off a missile, as Hub.dodge does: an attacker evading a
// gunner's missile.
func (a *Arena) dodge(id string, msg dodgeMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, r := a.playing(id)
	if p == nil || p.dead {
		return
	}
	b, ok := r.bullets[msg.MissileId]
	if !ok || !dodges(a.settings, p, b, a.now()) {
		return
	}
	p.lastEvade = a.now()
	delete(r.bullets, msg.MissileId)
	broadcast(r, missileLostMsg{Type: msgMissileLost, Id: msg.MissileId}, "")
}

// hit applies a hit reported by the shooter, as Hub.hit does, with teams:
// attackers can only hit the mothership, and gunners only attackers.
func (a *Arena) hit(id string, msg hitMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	shooter, r := a.playing(id)
	if shooter == nil {
		return
	}
	bulletId := id + ":" + msg.BulletId
	b, ok := r.bullets[bulletId]
	if !ok {
		return
	}
	elapsed := a.now().Sub(b.firedAt).Seconds()
	flight := math.Min(elapsed+latencyAllowance, float64(a.settings.BulletTtlMs)/1000)
	end := add(b.origin, mul(b.dir, flight*a.settings.BulletSpeed))
	if b.missile() && msg.Target != b.target {
		return
	}

	if msg.Target == mothershipTarget {
		// The mothership never moves, so there is no latency to allow for
		// in where it is: a small tolerance covers rounding. A missile
		// must only have been able to reach its hull.
		if shooter.team != TeamAttackers {
			return
		}
		if b.missile() {
			if !a.hull.sphereHits(b.origin, missileReach(a.settings, elapsed)+2) {
				return
			}
		} else if !a.hull.segmentHits(b.origin, end, 2) {
			return
		}
		delete(r.bullets, bulletId)
		shooter.hits++
		r.mothershipLife = math.Max(0, r.mothershipLife-a.settings.BulletDamage)
		broadcast(r, damageMsg{Type: msgDamage, Target: mothershipTarget, From: id, BulletId: bulletId, Life: r.mothershipLife}, "")
		if r.mothershipLife <= 0 {
			a.endRoundLocked(r, TeamAttackers)
		}
		return
	}

	target, ok := r.players[msg.Target]
	if !ok || target.dead || shooter.team != TeamGunners || target.team != TeamAttackers {
		return
	}
	if b.missile() {
		if !missileReaches(a.settings, b, target.pos, elapsed) {
			return
		}
	} else if distanceToSegment(target.pos, b.origin, end) > a.settings.ShipRadius+hitTolerance {
		return
	}
	delete(r.bullets, bulletId)
	target.life = math.Max(0, target.life-a.settings.BulletDamage)
	broadcast(r, damageMsg{Type: msgDamage, Target: target.id, From: id, BulletId: bulletId, Life: target.life}, "")
	if target.life > 0 {
		return
	}
	target.dead = true
	target.diedAt = a.now()
	target.deaths++
	shooter.kills++
	broadcast(r, diedMsg{Type: msgDied, Target: target.id, From: id}, "")
}

// crash destroys an attacker that flew into the mothership, or two attackers
// that ran into each other, when the server's record of where they were
// agrees. The mothership takes no damage from it, and nobody gets a kill.
func (a *Arena) crash(id string, msg crashMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, r := a.playing(id)
	if p == nil || p.dead || p.team != TeamAttackers {
		return
	}
	victims := []*Player{p}
	if msg.With == mothershipTarget {
		if !a.hull.sphereHits(p.pos, a.settings.ShipRadius+hitTolerance) {
			return
		}
	} else {
		other, ok := r.players[msg.With]
		if !ok || other == p || other.dead || other.team != TeamAttackers ||
			!shipsTouch(p.pos, other.pos, a.settings.ShipRadius) {
			return
		}
		victims = append(victims, other)
	}
	for _, ship := range victims {
		ship.life = 0
		ship.dead = true
		ship.diedAt = a.now()
		ship.deaths++
		broadcast(r, diedMsg{Type: msgDied, Target: ship.id}, "")
	}
}

func (a *Arena) respawn(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, r := a.playing(id)
	if p == nil || !p.dead {
		return
	}
	if a.now().Sub(p.diedAt) < time.Duration(a.settings.RespawnDelayMs)*time.Millisecond {
		return
	}
	p.dead = false
	p.life = a.rules.AttackerLife
	p.pos, p.rot = a.attackerSpawn()
	p.vel = vec3{}
	broadcast(r, respawnedMsg{Type: msgRespawned, Id: id, Life: p.life, P: p.pos, Q: p.rot}, "")
}

// PlayerCount is the number of players connected to the assault mode, in
// lobbies and rounds.
func (a *Arena) PlayerCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.rooms)
}

// broadcast queues msg for everybody in a room except `except`. Queueing
// never blocks, so it is safe under the arena's lock.
func broadcast(r *room, msg any, except string) {
	frame, err := json.Marshal(msg)
	if err != nil {
		log.Println("assault: marshal:", err)
		return
	}
	for id, p := range r.players {
		if id != except {
			p.client.enqueue(frame)
		}
	}
}
