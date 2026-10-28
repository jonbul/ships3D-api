package game

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMissileHomesOnItsTargetOnly(t *testing.T) {
	s := newTestServer(t)
	shooter := s.connect(t, "?user=shooter")
	shooterId := shooter.join("default:arrow", "")
	target := s.connect(t, "?user=target")
	targetId := target.join("default:hammer", "")
	bystander := s.connect(t, "?user=bystander")
	bystanderId := bystander.join("default:arrow", "")

	shooter.place([3]float64{0, 0, 0})
	target.place([3]float64{0, 0, 500})
	bystander.place([3]float64{0, 0, 20})

	settings := DefaultSettings()
	s.clock.Advance(time.Second)
	shooter.send(map[string]any{"type": msgMissile, "id": "m1", "target": targetId, "p": []float64{0, 0, 0}, "d": []float64{0, 0, 1}})
	launched := target.expect(msgMissile)
	if launched["owner"] != shooterId || launched["target"] != targetId || launched["id"] != shooterId+":m1" {
		t.Fatalf("missile = %v", launched)
	}

	// At once: a missile can't have flown 500 units yet.
	shooter.send(map[string]any{"type": msgHit, "bulletId": "m1", "target": targetId})
	target.expectNone(msgDamage)
	// Nor hit anybody but its target, however close.
	shooter.send(map[string]any{"type": msgHit, "bulletId": "m1", "target": bystanderId})
	bystander.expectNone(msgDamage)

	s.clock.Advance(3 * time.Second)
	shooter.send(map[string]any{"type": msgHit, "bulletId": "m1", "target": targetId})
	damage := target.expect(msgDamage)
	if want := settings.MaxLife - settings.MissileDamage; damage["life"] != want || damage["bulletId"] != shooterId+":m1" {
		t.Fatalf("damage = %v, want life %v", damage, want)
	}
	// Used up.
	shooter.send(map[string]any{"type": msgHit, "bulletId": "m1", "target": targetId})
	target.expectNone(msgDamage)
}

func TestMissileCooldownRangeAndFuel(t *testing.T) {
	s := newTestServer(t)
	shooter := s.connect(t, "?user=shooter")
	shooter.join("default:arrow", "")
	target := s.connect(t, "?user=target")
	targetId := target.join("default:hammer", "")
	shooter.place([3]float64{0, 0, 0})
	target.place([3]float64{0, 0, 300})

	settings := DefaultSettings()
	fire := func(id string) {
		shooter.send(map[string]any{"type": msgMissile, "id": id, "target": targetId, "p": []float64{0, 0, 0}, "d": []float64{0, 0, 1}})
	}
	s.clock.Advance(time.Second)
	fire("1")
	target.expect(msgMissile)

	// Still reloading.
	s.clock.Advance(time.Second)
	fire("2")
	target.expectNone(msgMissile)

	// Reloaded, but the target is out of range.
	s.clock.Advance(time.Duration(settings.MissileCooldownMs) * time.Millisecond)
	target.place([3]float64{0, 0, settings.LockRange * 2})
	fire("3")
	target.expectNone(msgMissile)

	// Missile 1 ran out of fuel long ago: forgotten, so it can't hit.
	s.hub.tick()
	shooter.send(map[string]any{"type": msgHit, "bulletId": "1", "target": targetId})
	target.expectNone(msgDamage)

	// Not at yourself.
	target.place([3]float64{0, 0, 300})
	shooter.send(map[string]any{"type": msgMissile, "id": "4", "target": "nobody", "p": []float64{0, 0, 0}, "d": []float64{0, 0, 1}})
	target.expectNone(msgMissile)
	fire("5")
	target.expect(msgMissile)
}

func TestMissilesInTheAssault(t *testing.T) {
	s := newArenaServer(t, nil)
	clients, starts := s.startRound(t, 4)
	gunner, attacker, other := clients[0], clients[1], clients[2]
	attackerId := starts[1]["id"].(string)
	if starts[1]["settings"].(map[string]any)["missileDamage"] != float64(1) {
		t.Fatalf("a missile should be one hit in the assault: %v", starts[1]["settings"])
	}

	// An attacker's missile homes on the mothership, and is one hit.
	attacker.place([3]float64{0, 0, -400})
	s.clock.Advance(time.Second)
	attacker.send(map[string]any{"type": msgMissile, "id": "m", "target": mothershipTarget, "p": []float64{0, 0, -400}, "d": []float64{0, 0, 1}})
	other.expect(msgMissile)
	attacker.send(map[string]any{"type": msgHit, "bulletId": "m", "target": mothershipTarget})
	other.expectNone(msgDamage) // Too soon to have got there.
	s.clock.Advance(3 * time.Second)
	attacker.send(map[string]any{"type": msgHit, "bulletId": "m", "target": mothershipTarget})
	if damage := other.expect(msgDamage); damage["life"] != DefaultAssaultRules().MothershipLife-1 {
		t.Fatalf("damage: %v", damage)
	}

	// Attackers can't lock on to each other, nor gunners on the mothership.
	s.clock.Advance(time.Minute)
	attacker.send(map[string]any{"type": msgMissile, "id": "n", "target": starts[2]["id"], "p": []float64{0, 0, -400}, "d": []float64{0, 0, 1}})
	seat := starts[0]["players"].([]any)[0].(map[string]any)["seat"].(map[string]any)["p"].([]any)
	from := []float64{seat[0].(float64), seat[1].(float64), seat[2].(float64)}
	gunner.send(map[string]any{"type": msgMissile, "id": "x", "target": mothershipTarget, "p": from, "d": []float64{0, 0, 1}})
	other.expectNone(msgMissile)

	// A gunner's homes on an attacker.
	attacker.place([3]float64{from[0], from[1], from[2] + 200})
	gunner.send(map[string]any{"type": msgMissile, "id": "g", "target": attackerId, "p": from, "d": []float64{0, 0, 1}})
	attacker.expect(msgMissile)
	s.clock.Advance(2 * time.Second)
	gunner.send(map[string]any{"type": msgHit, "bulletId": "g", "target": attackerId})
	if damage := attacker.expect(msgDamage); damage["life"] != DefaultAssaultRules().AttackerLife-1 {
		t.Fatalf("damage: %v", damage)
	}
}

func TestMissileFieldNames(t *testing.T) {
	// Pins the wire names ships-vue-3d reads (src/game/protocol.ts).
	frame, _ := json.Marshal(missileOutMsg{Type: msgMissile, Id: "i", Owner: "o", Target: "t", P: vec3{1, 2, 3}, D: vec3{0, 0, 1}})
	want := `{"type":"missile","id":"i","owner":"o","target":"t","p":[1,2,3],"d":[0,0,1]}`
	if string(frame) != want {
		t.Errorf("missile = %s, want %s", frame, want)
	}
}

func TestEvasiveRollShakesOffAMissileInTheLastMoment(t *testing.T) {
	s := newTestServer(t)
	shooter := s.connect(t, "?user=shooter")
	shooterId := shooter.join("default:arrow", "")
	target := s.connect(t, "?user=target")
	targetId := target.join("default:hammer", "")
	bystander := s.connect(t, "?user=bystander")
	bystander.join("default:arrow", "")
	shooter.place([3]float64{0, 0, 0})
	target.place([3]float64{0, 0, 600})

	s.clock.Advance(time.Second)
	shooter.send(map[string]any{"type": msgMissile, "id": "m", "target": targetId, "p": []float64{0, 0, 0}, "d": []float64{0, 0, 1}})
	missileId := target.expect(msgMissile)["id"]
	if missileId != shooterId+":m" {
		t.Fatalf("missile id %v", missileId)
	}

	// Too early: the missile is still far away. Nobody else can dodge it.
	target.send(map[string]any{"type": msgDodge, "missileId": missileId})
	bystander.send(map[string]any{"type": msgDodge, "missileId": missileId})
	shooter.expectNone(msgMissileLost)

	// Close: shaken off, and it can no longer hit.
	s.clock.Advance(2500 * time.Millisecond)
	target.send(map[string]any{"type": msgDodge, "missileId": missileId})
	if lost := shooter.expect(msgMissileLost); lost["id"] != missileId {
		t.Fatalf("missileLost = %v", lost)
	}
	bystander.expect(msgMissileLost)
	shooter.send(map[string]any{"type": msgHit, "bulletId": "m", "target": targetId})
	target.expectNone(msgDamage)

	// The roll needs to reload before it shakes off another.
	s.clock.Advance(time.Duration(DefaultSettings().MissileCooldownMs) * time.Millisecond)
	shooter.send(map[string]any{"type": msgMissile, "id": "n", "target": targetId, "p": []float64{0, 0, 0}, "d": []float64{0, 0, 1}})
	second := target.expect(msgMissile)["id"]
	s.clock.Advance(3 * time.Second)
	target.send(map[string]any{"type": msgDodge, "missileId": second})
	shooter.expect(msgMissileLost)

	// Another missile, from close by, straight after: the roll hasn't
	// reloaded yet; once it has, it works.
	bystander.place([3]float64{0, 0, 500})
	bystander.send(map[string]any{"type": msgMissile, "id": "o", "target": targetId, "p": []float64{0, 0, 500}, "d": []float64{0, 0, 1}})
	third := target.expect(msgMissile)["id"]
	target.send(map[string]any{"type": msgDodge, "missileId": third})
	shooter.expectNone(msgMissileLost)
	s.clock.Advance(3 * time.Second)
	target.send(map[string]any{"type": msgDodge, "missileId": third})
	shooter.expect(msgMissileLost)
}
