package game

// The websocket protocol. Every message, both ways, is one JSON object with a
// "type" field. It is mirrored by hand in ships-vue-3d (src/game/protocol.ts):
// change both together.
//
// Client -> server:
//   join     {shipId, name}      once, right after connecting
//   state    {p, q, v}           own position/orientation/velocity, ~20/s
//   fire     {id, p, d}          a bullet fired from p along unit vector d
//   missile  {id, target, p, d}  a homing missile fired from p, nose along
//                                d, at the ship we locked on to: `target`
//   hit      {bulletId, target}  one of our bullets (or missiles) hit `target`
//   dodge    {missileId}         we did an evasive roll with missile
//                                `missileId` (its full id, as received) close
//   respawn  {}                  after death, once respawnDelayMs has passed
//   crash    {with}              our ship ran into ship `with` (or, in the
//                                assault, "mothership"): see crashed below
//
// Server -> client:
//   welcome      {id, settings, players}  answer to join; players includes us
//   playerJoined {player}
//   playerLeft   {id}
//   snapshot     {players: [{id, p, q, v}]}  moved players, every tick
//   fire         {id, owner, p, d}           a bullet someone else fired
//   missile      {id, owner, target, p, d}   a missile someone else fired
//   missileLost  {id}                        a missile was shaken off: it
//                                            flies straight on, harmless
//   damage       {target, from, bulletId, life}
//   died         {target, from}
//   respawned    {id, life, p, q}
//   error        {message}

const (
	msgJoin    = "join"
	msgState   = "state"
	msgFire    = "fire"
	msgMissile = "missile"
	msgDodge   = "dodge"
	msgHit     = "hit"
	msgRespawn = "respawn"
	msgCrash   = "crash"

	msgWelcome      = "welcome"
	msgPlayerJoined = "playerJoined"
	msgPlayerLeft   = "playerLeft"
	msgSnapshot     = "snapshot"
	msgDamage       = "damage"
	msgMissileLost  = "missileLost"
	msgDied         = "died"
	msgRespawned    = "respawned"
	msgError        = "error"
)

type envelope struct {
	Type string `json:"type"`
}

type joinMsg struct {
	ShipId string `json:"shipId"`
	Name   string `json:"name"`
}

type stateMsg struct {
	P vec3 `json:"p"`
	Q quat `json:"q"`
	V vec3 `json:"v"`
}

type fireMsg struct {
	Id string `json:"id"`
	P  vec3   `json:"p"`
	D  vec3   `json:"d"`
}

type missileMsg struct {
	Id     string `json:"id"`
	Target string `json:"target"`
	P      vec3   `json:"p"`
	D      vec3   `json:"d"`
}

type dodgeMsg struct {
	MissileId string `json:"missileId"`
}

type missileLostMsg struct {
	Type string `json:"type"`
	Id   string `json:"id"`
}

type crashMsg struct {
	With string `json:"with"`
}

type hitMsg struct {
	BulletId string `json:"bulletId"`
	Target   string `json:"target"`
}

// PlayerInfo is everything a client needs to show a player, sent once when
// they join (or when we join); after that only snapshots, damage and deaths
// update it.
type PlayerInfo struct {
	Id     string   `json:"id"`
	Name   string   `json:"name"`
	Guest  bool     `json:"guest"`
	Ship   ShipInfo `json:"ship"`
	Scale  float64  `json:"scale"`
	Life   float64  `json:"life"`
	Dead   bool     `json:"dead"`
	Kills  int      `json:"kills"`
	Deaths int      `json:"deaths"`
	P      vec3     `json:"p"`
	Q      quat     `json:"q"`
	V      vec3     `json:"v"`
	// Assault mode only: the player's team, hits on the mothership (by an
	// attacker), and a gunner's seat.
	Team string `json:"team,omitempty"`
	Hits int    `json:"hits,omitempty"`
	Seat *Seat  `json:"seat,omitempty"`
}

type welcomeMsg struct {
	Type     string       `json:"type"`
	Id       string       `json:"id"`
	Settings Settings     `json:"settings"`
	Players  []PlayerInfo `json:"players"`
}

type playerJoinedMsg struct {
	Type   string     `json:"type"`
	Player PlayerInfo `json:"player"`
}

type playerLeftMsg struct {
	Type string `json:"type"`
	Id   string `json:"id"`
}

type playerState struct {
	Id string `json:"id"`
	P  vec3   `json:"p"`
	Q  quat   `json:"q"`
	V  vec3   `json:"v"`
}

type snapshotMsg struct {
	Type    string        `json:"type"`
	Players []playerState `json:"players"`
}

type fireOutMsg struct {
	Type  string `json:"type"`
	Id    string `json:"id"`
	Owner string `json:"owner"`
	P     vec3   `json:"p"`
	D     vec3   `json:"d"`
}

type missileOutMsg struct {
	Type   string `json:"type"`
	Id     string `json:"id"`
	Owner  string `json:"owner"`
	Target string `json:"target"`
	P      vec3   `json:"p"`
	D      vec3   `json:"d"`
}

type damageMsg struct {
	Type     string  `json:"type"`
	Target   string  `json:"target"`
	From     string  `json:"from"`
	BulletId string  `json:"bulletId"`
	Life     float64 `json:"life"`
}

type diedMsg struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	From   string `json:"from"`
}

type respawnedMsg struct {
	Type string  `json:"type"`
	Id   string  `json:"id"`
	Life float64 `json:"life"`
	P    vec3    `json:"p"`
	Q    quat    `json:"q"`
}

type errorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// The assault mode (/ws/assault) uses the same messages in a round, and
// these around it. A hit on the mothership has target "mothership", and so
// do the damage messages it causes; an attacker's missile can home on it too.
//
// Server -> client:
//   lobby       {id, players: [{id, name, guest}], minPlayers, maxPlayers, startsInMs?}
//               on joining and whenever the lobby changes; startsInMs only
//               while it is counting down
//   roundStart  {id, settings, rules, mothership, players, endsInMs}
//               players carry their team, and gunners their seat
//   roundOver   {winner, returnInMs}  winner is "attackers" or "gunners"

const (
	msgLobby      = "lobby"
	msgRoundStart = "roundStart"
	msgRoundOver  = "roundOver"

	TeamAttackers = "attackers"
	TeamGunners   = "gunners"

	mothershipTarget = "mothership"
)

type lobbyPlayer struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Guest bool   `json:"guest"`
}

type lobbyMsg struct {
	Type       string        `json:"type"`
	Id         string        `json:"id"`
	Players    []lobbyPlayer `json:"players"`
	MinPlayers int           `json:"minPlayers"`
	MaxPlayers int           `json:"maxPlayers"`
	StartsInMs *int64        `json:"startsInMs,omitempty"`
}

type MothershipInfo struct {
	Ship    ShipInfo `json:"ship"`
	Scale   float64  `json:"scale"`
	Life    float64  `json:"life"`
	MaxLife float64  `json:"maxLife"`
}

type roundStartMsg struct {
	Type       string         `json:"type"`
	Id         string         `json:"id"`
	Settings   Settings       `json:"settings"`
	Rules      AssaultRules   `json:"rules"`
	Mothership MothershipInfo `json:"mothership"`
	Players    []PlayerInfo   `json:"players"`
	EndsInMs   int64          `json:"endsInMs"`
}

type roundOverMsg struct {
	Type       string `json:"type"`
	Winner     string `json:"winner"`
	ReturnInMs int64  `json:"returnInMs"`
}
