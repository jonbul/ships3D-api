package game

// Settings are the rules of the game. The server is their single source of
// truth: they are sent to every client in the `welcome` message, so the
// browser's flight model and hit tests can never drift from the values the
// server validates against. Units are world units, seconds and milliseconds.
type Settings struct {
	TickRate int `json:"tickRate"`
	// WorldRadius bounds the playable space: a sphere around the origin.
	WorldRadius float64 `json:"worldRadius"`
	// ShipRadius is the size every ship is normalised to, whatever its design
	// measures, and also its hit sphere. Normalising keeps a huge design from
	// being an easy target and a tiny one from being unhittable.
	ShipRadius float64 `json:"shipRadius"`
	MaxLife    float64 `json:"maxLife"`
	// Flight model, per second. Speed is along the ship's nose; reverse is
	// allowed but slower.
	MaxSpeed     float64 `json:"maxSpeed"`
	MinSpeed     float64 `json:"minSpeed"`
	Acceleration float64 `json:"acceleration"`
	PitchRate    float64 `json:"pitchRate"`
	YawRate      float64 `json:"yawRate"`
	RollRate     float64 `json:"rollRate"`
	// Weapons.
	BulletSpeed    float64 `json:"bulletSpeed"`
	BulletTtlMs    int     `json:"bulletTtlMs"`
	BulletDamage   float64 `json:"bulletDamage"`
	FireCooldownMs int     `json:"fireCooldownMs"`
	RespawnDelayMs int     `json:"respawnDelayMs"`
	// Homing missiles. A ship locks on to an enemy by keeping it within
	// LockAngle (radians) of its nose, and closer than LockRange, for
	// LockTimeMs; it may then fire one missile every MissileCooldownMs. The
	// missile flies at MissileSpeed, turns towards its target at up to
	// MissileTurnRate radians a second, and runs out of fuel after
	// MissileTtlMs.
	LockRange         float64 `json:"lockRange"`
	LockAngle         float64 `json:"lockAngle"`
	LockTimeMs        int     `json:"lockTimeMs"`
	MissileSpeed      float64 `json:"missileSpeed"`
	MissileTurnRate   float64 `json:"missileTurnRate"`
	MissileTtlMs      int     `json:"missileTtlMs"`
	MissileDamage     float64 `json:"missileDamage"`
	MissileCooldownMs int     `json:"missileCooldownMs"`
	// Evasion. A ship can do an evasive barrel roll every EvadeCooldownMs;
	// one done while a missile homing on it is within DodgeRange shakes the
	// missile off. A 180° turn can be done every TurnCooldownMs (the server
	// doesn't see it: it is just flying).
	DodgeRange      float64 `json:"dodgeRange"`
	EvadeCooldownMs int     `json:"evadeCooldownMs"`
	TurnCooldownMs  int     `json:"turnCooldownMs"`
}

func DefaultSettings() Settings {
	return Settings{
		TickRate:       20,
		WorldRadius:    1500,
		ShipRadius:     4,
		MaxLife:        10,
		MaxSpeed:       80,
		MinSpeed:       -20,
		Acceleration:   40,
		PitchRate:      1.4,
		YawRate:        1.0,
		RollRate:       2.2,
		BulletSpeed:    300,
		BulletTtlMs:    2000,
		BulletDamage:   1,
		FireCooldownMs: 150,
		RespawnDelayMs: 3000,

		LockRange:         700,
		LockAngle:         0.2,
		LockTimeMs:        2000,
		MissileSpeed:      150,
		MissileTurnRate:   2.5,
		MissileTtlMs:      8000,
		MissileDamage:     3,
		MissileCooldownMs: 10000,

		DodgeRange:      200,
		EvadeCooldownMs: 3000,
		TurnCooldownMs:  4000,
	}
}

// AssaultRules are the rules of the assault mode, on top of Settings: two
// teams around a mothership at the centre of the world. Attackers fly ships
// and try to destroy it; gunners sit in turrets on it and shoot them down.
// Players wait in a lobby until enough of them are there to start a round.
type AssaultRules struct {
	// A lobby starts its round once it has at least MinPlayers and nobody
	// has joined for LobbyWaitMs, or at once when it reaches MaxPlayers.
	MinPlayers  int `json:"minPlayers"`
	MaxPlayers  int `json:"maxPlayers"`
	LobbyWaitMs int `json:"lobbyWaitMs"`
	// AttackersPerGunner sets the teams: one gunner for about this many
	// attackers, and always at least one.
	AttackersPerGunner int `json:"attackersPerGunner"`
	// Hits an attacker's ship takes (it replaces Settings.MaxLife in a
	// round), and hits the mothership takes.
	AttackerLife   float64 `json:"attackerLife"`
	MothershipLife float64 `json:"mothershipLife"`
	// MothershipRadius is the size the mothership's design is scaled to,
	// as ShipRadius is for the attackers' ships.
	MothershipRadius float64 `json:"mothershipRadius"`
	// Reloads: the attackers' (it replaces Settings.FireCooldownMs in a
	// round) and the turrets'.
	AttackerCooldownMs int `json:"attackerCooldownMs"`
	GunnerCooldownMs   int `json:"gunnerCooldownMs"`
	// GunnerTurnRate is how fast a turret turns, in radians a second.
	GunnerTurnRate float64 `json:"gunnerTurnRate"`
	// SpawnDistance is how far from the mothership attackers appear.
	SpawnDistance float64 `json:"spawnDistance"`
	// A round the mothership survives for RoundMs is won by the gunners.
	RoundMs int `json:"roundMs"`
	// ResultsMs is how long the result is shown before everybody is back in
	// the lobby.
	ResultsMs int `json:"resultsMs"`
}

func DefaultAssaultRules() AssaultRules {
	return AssaultRules{
		MinPlayers:         4,
		MaxPlayers:         20,
		LobbyWaitMs:        30000,
		AttackersPerGunner: 3,
		AttackerLife:       3,
		MothershipLife:     100,
		MothershipRadius:   180,
		AttackerCooldownMs: 600,
		GunnerCooldownMs:   150,
		GunnerTurnRate:     1.6,
		SpawnDistance:      1000,
		RoundMs:            5 * 60 * 1000,
		ResultsMs:          10000,
	}
}
