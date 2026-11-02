package game

import (
	"math"
	"time"
)

// Homing missiles. Like bullets, the shooter simulates its missiles and
// reports their hits, and the server only checks that a report is plausible.
// A missile turns towards its target as it flies, so its path can't be
// replayed here the way a bullet's straight line is; instead the server
// checks what no homing can get round: the missile only ever hits the ship
// it was fired at, and can't have flown further than its speed allows.
//
// Locking on happens in the shooter's browser and isn't checked, beyond the
// target being in range when the missile is fired: the cooldown is what
// keeps missiles rare.

// missile reports whether this is a missile (homing on target) rather than a
// bullet.
func (b *bullet) missile() bool { return b.target != "" }

// projectileTtl is how long a bullet or missile is remembered: its flight
// time plus the allowance for a hit report arriving late.
func projectileTtl(s Settings, b *bullet) time.Duration {
	ms := s.BulletTtlMs
	if b.missile() {
		ms = s.MissileTtlMs
	}
	return time.Duration(ms)*time.Millisecond + time.Duration(latencyAllowance*float64(time.Second))
}

// missileCooldownOver reports whether a player may fire a missile now, with
// the same 20% leeway as bullets (see Hub.fire).
func missileCooldownOver(s Settings, p *Player, now time.Time) bool {
	cooldown := time.Duration(float64(s.MissileCooldownMs)*0.8) * time.Millisecond
	return now.Sub(p.lastMissile) >= cooldown
}

// missileOriginOk reports whether a missile leaves from roughly where its
// shooter is, as for bullets.
func missileOriginOk(s Settings, p *Player, origin vec3) bool {
	return finiteVec(origin) && length(sub(origin, p.pos)) <= s.ShipRadius*3+s.MaxSpeed*0.5
}

// inLockRange reports whether a target `distance` away from the shooter can
// have been locked on to: positions are up to a round trip late, and both
// ships may have moved since.
func inLockRange(s Settings, distance float64) bool {
	return distance <= s.LockRange+hitTolerance+s.MaxSpeed
}

// dodges reports whether player p, doing an evasive roll now, shakes off
// missile b: it must be homing on them, their roll must have reloaded (with
// the usual 20% leeway), and the missile must have been able to get within
// DodgeRange of them - the roll only works in the last moment.
func dodges(s Settings, p *Player, b *bullet, now time.Time) bool {
	if b.target != p.id {
		return false
	}
	cooldown := time.Duration(float64(s.EvadeCooldownMs)*0.8) * time.Millisecond
	if now.Sub(p.lastEvade) < cooldown {
		return false
	}
	reach := missileReach(s, now.Sub(b.firedAt).Seconds())
	return length(sub(p.pos, b.origin)) <= reach+s.DodgeRange+s.ShipRadius+hitTolerance
}

// missileReach is the furthest a missile can be from where it was fired,
// `elapsed` seconds after it was, allowing for a late report.
func missileReach(s Settings, elapsed float64) float64 {
	flight := math.Min(elapsed+latencyAllowance, float64(s.MissileTtlMs)/1000)
	return flight * s.MissileSpeed
}

// missileReaches reports whether a missile can have hit a ship at target by
// now: whatever it turned, it can't have flown further than missileReach.
func missileReaches(s Settings, b *bullet, target vec3, elapsed float64) bool {
	return length(sub(target, b.origin)) <= missileReach(s, elapsed)+s.ShipRadius+hitTolerance
}
