CHANGES
=======
Version 0.10.0 - 2026-10-28
------------------
Shaking off missiles, for ships-vue-3d 0.11.0.

- A new message, `dodge {missileId}`: the player did an evasive roll with
  that missile (its full id, as received in `missile`) close behind. If the
  missile is homing on them, could by now be within `dodgeRange` (200 u) of
  them, and their roll has reloaded (`evadeCooldownMs`, 3 s), it is shaken
  off: forgotten, so it can't hit any more, and everybody gets
  `missileLost {id}` to let it fly on, harmless. A roll done too early does
  nothing.
- `turnCooldownMs` (4 s) for the browser's new 180° turn; the server doesn't
  check it, since it is just flying.

Version 0.9.0 - 2026-10-27
------------------
Homing missiles, for ships-vue-3d 0.10.0.

- A new message, `missile {id, target, p, d}`: a missile fired at the ship
  the player locked on to. Everybody else gets it as `missile {id, owner,
  target, p, d}`. Its hit is reported with the usual `hit`, its id as the
  `bulletId`, and is answered with the usual `damage` (and `died`).
- Locking on is up to the browser (keeping an enemy close to the nose, within
  `lockAngle` and `lockRange`, for `lockTimeMs`). The server checks that the
  target is alive, in range and somebody the player may shoot, and that the
  missile has reloaded: one every 10 seconds.
- A missile flies at 150 u/s (ships top out at 80), turns towards its
  target at up to 2.5 radians a second, and runs out of fuel after 8
  seconds. Since it doesn't fly straight, a hit is accepted when it is on
  the ship the missile was fired at and no further away than the missile
  could have flown.
- A missile takes 3 life (bullets: 1). In the assault it is one hit, like a
  bullet: an attacker's homes on the mothership, a gunner's on an attacker.
- The new rules are in `Settings`: `lockRange`, `lockAngle`, `lockTimeMs`,
  `missileSpeed`, `missileTurnRate`, `missileTtlMs`, `missileDamage` and
  `missileCooldownMs`.

Five new built-in ships, so guests have more to choose from: Kestrel (a
fighter), Mule (a freighter), Lancer (an interceptor), Bulwark (a gunship) and
Wisp (a scout).

Version 0.8.0 - 2026-10-20
------------------
Imported 3D models as ship parts. Needs ships-vue-3d 0.8.0.

- `POST /models?name=<file name>` stores a `.glb` file sent as the request
  body, for the logged-in user, and answers with its id. Importing the same
  file again returns the first one. `GET /models/:id` serves it to anyone
  (every player loads the models of the ships they see), cached for good
  since a model never changes.
- Models must be binary glTF 2.0, at most 4 MB and 100,000 triangles, with
  everything embedded (no links to other files) and no Draco, meshopt or
  Basis compression, which the game can't decode. Up to 100 models per user.
  They are kept in a new `models3d` collection.
- A new part type, `model`, with a `modelId` naming the model it shows. A
  model keeps its own look, so it has no texture or image.

Version 0.7.0 - 2026-10-20
------------------
Ship designs accept lights and see-through parts, for ships-vue-3d 0.7.0.

- A new part type, `light`, with an `intensity` from 0.1 to 10. Its `color`
  is the light's color. A project can have at most 8 lights, a mirrored one
  counting twice. Other parts' `intensity` is dropped on save.
- Every part may have an `opacity` from 0.05 to 1; left out, it is opaque as
  before.

Version 0.6.0 - 2026-10-19
------------------
A new game mode, the assault, on its own websocket: `/ws/assault`. The free
for all at `/ws` is unchanged.

- Two teams and a mothership at the centre of the world. Attackers fly their
  ships and try to destroy it; gunners sit in turrets on its hull and shoot
  the attackers down. An attacker's ship takes 3 hits, and respawns after the
  usual delay; the mothership takes 100. Attackers can only hit the
  mothership and gunners only attackers: there is no friendly fire.
- The attackers win by destroying the mothership. The gunners win if it is
  still flying after 5 minutes, or the attackers all leave (and the other
  way round).
- Teams are dealt at random when a round starts: one gunner for about three
  attackers (4 players: 1 gunner, 20 players: 5), and always at least one.
  The turrets are spread around the ship, alternating between its top and
  its underside.
- Players wait in a lobby. It starts its round 30 seconds after the last
  player joined, once there are at least 4; with 20 it starts at once. A new
  lobby then opens for the next players. When a round ends, its result is
  shown for 10 seconds and then all its players go back to the open lobby
  together.
- Hits on the mothership are checked against its actual shape (every part's
  box), not a sphere: it never moves, so the server knows exactly where it is.
- The rules (players, waits, lives, reloads, round length) are in
  `AssaultRules`, sent to the players with each round.
- `/status` also counts the players in the assault mode.

Crashes, in both modes: a new client message, `crash {with}`, reports that
our ship ran into ship `with` (or `"mothership"` in the assault). The server
destroys both ships if its own record of their positions puts their hit
spheres within reach of each other (allowing for latency, as for hits), or
the attacker if it is at the mothership's hull. Gunners never crash, the
mothership takes no damage from one, and nobody gets a kill: `died` comes
with an empty `from`.

Version 0.5.0 - 2026-10-19
------------------
Messages for players in Spanish and Basque (Euskara), as well as English.

- Error messages from the API (login, registration, saving a ship...) are in
  the language of the request's Accept-Language header, which ships-vue-3d
  sets to the language picked on the site. Anything other than `es` or `eu`
  gets English, as before.
- The game websocket takes the language as a `lang` query parameter
  (`/ws?lang=eu`), since browsers can't set headers there. It applies to the
  game's error messages and to the default name for guests.
- CORS allows the Accept-Language header.
- The texture and image link errors from 0.4.0 are translated too.

Version 0.4.0 - 2026-10-19
------------------
Ship parts can have a texture: `texture` on a part is one of `panels`,
`grid`, `stripes`, `carbon`, `hexagons`, `worn`, `camo` or `windows`, or is
left out for a plain part. It is saved with the ship and sent to every player
in game; ships saved before stay plain.

A part can instead have `textureUrl`, an image link used as its texture. It
must be https, with a host, no spaces or control characters and at most 500
characters, and a part can't have both `texture` and `textureUrl`.

Version 0.3.0 - 2026-10-18
------------------
Six new part types for the ship designer: dome, capsule, pyramid, tube,
prism (hexagonal) and frustum (tapered cylinder). Ships using them can now be
saved and flown.

Version 0.2.0 - 2026-10-10
------------------
Players on mobile data are no longer thrown out of the game after a few
seconds.

Bugfixes
- Flood protection is now a token bucket (60 messages a second sustained,
  bursts of up to 400) instead of a hard 120 per second. A mobile connection
  that stalls delivers everything the browser queued meanwhile in one go,
  and that catch-up burst was being treated as a flood. A client that is cut
  off is now told why before its connection closes.

Version 0.1.0 - 2026-10-09
------------------
First version: the backend of Ships 3D, a 3D take on the Ships game where
players design ships from 3D primitives and fly them in open space.

- Users and sessions are shared with the 2D game (`users` and `sessions`
  collections, same `token` cookie), so one account and one login work in
  both. Registration, login (with "remember me"), logout and password change.
- 3D projects in their own `paintingProjects3d` collection, with a REST API
  (`/projects`) that only ever lets a user see or change their own projects,
  and server-side validation of every design.
- Real-time game over `/ws`: client-authoritative flight, server-authoritative
  life, kills, deaths and respawns. Reported hits are checked against the
  server's record of the bullet and the target before any damage applies.
- Built-in ships (Arrow, Hammer, Saucer) for guests and new players.
