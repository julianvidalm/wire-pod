### Why each keyphrase changed (robot-wake, 2026-10-20, decided on natural sentences only)

The live rule also changed: keyphrases must begin at a word (fixes `no` in "Buenos días.", `correcto` in "Incorrecto.",
`ven` in "Inventa algo."), an exact match compares words so whisper's punctuation does not hide it ("No." is `no`),
and the intent is the one with the longest keyphrase in the question ("Gira a la izquierda." is turn left, not
`gira`). Phrases were lengthened or added rather than dropped; the four drops answered no natural command.

**en, removed**
- `how old` (character_age): took "How old is the universe?"; the commands keep `how old are you`, `how old you are`, `old are you`.
- `i never` (seasonal_happynewyear): took "I never thought about that."; a whisper guess for "happy new year", no natural hit.
- `what time` (clock_time): took "What time zone is Paris in?"; the commands keep `what time is it`, `time is it`, `the time`.
- `for you` (greeting_hello): took the command "Look for your cube."; a whisper guess, no natural hit.

**en, added** (natural commands the lists lost, or another intent's shorter keyphrase stole)
- `how old are you`, `how old you are`, `your age` (character_age): "Tell me how old you are.", "What's your age?".
- `look for your cube` (findcube), `roll the cube` (rollcube), `pick up the cube`, `pickup the cube` (pickupcube): `the cu` and `for you` stole them.
- `go explore` (explore_start): "Go explore." was lost and `the ro` (praise) stole "Go explore the room."; `plor` no longer matches inside "explore" (word start).
- `turn left`, `turn right`: `rn left` / `rn right` begin inside "turn", so with word start the plain commands need them.
- `be quieter` (volumedown): `be quiet` (quiet) stole it. `change your volume`, `set the volume` (volumelevel): `change` (eyecolor) stole one, `volume` was too short.
- `left on the timer` (checktimer), `timer for` (settimer), `see you later` (goodbye), `dance for me` (dance), `color of your eyes` (eyecolor), `drive forward` (forward), `a trick` (anytrick), `card game` (blackjack), `fist bump` (fistbump: the lists had only `fistbump`, `fist bomb`, `fist ball`), `your charger` (charger), `weather like today`, `weather today`, `will it rain` (weather; not `the weather`, which would take "What's the weather like on Mars?"), `ask you a question` (promptquestion), `play my message`, `record a message`, `call me` (username), `take a selfie` (photo): natural commands whose only keyphrase covered less than 0.3 of them.

**es, removed**
- `buenas tardes` (goodnight) and `buenos días`, `buenas noches`, `buenas tardes` (hello): the same greetings sat in two or three intents, so the first in list order won: "Buenos días." was hello, "Buenas tardes." goodnight. Each greeting now lives in one intent: `buenos días`, `buenas tardes` in goodmorning (like en `good morning`, `afternoon`), `buenas noches` in goodnight.

**es, added**
- `buenos días`, `buen día`, `buenas tardes` (goodmorning): see above; "Buen día." was lost (`dia` has no accent).
- `cómo me llamo` (names_ask): the list had `como me llamo` without the accent, so `me llamo` (username) took "¿Cómo me llamo?".
- `hacer una pregunta` (promptquestion): `te quiero` (love) took "Te quiero hacer una pregunta.".
- `baja el volumen` (volumedown; the list had only the formal `baje el volumen`) and `más volumen` (volumeup): `volumen` (volumelevel) took both.
- `otra carta` (hit), `me planto` (stand), `comprueba el temporizador`, `comprueba el cronómetro`, `cuánto falta` (checktimer; the list had the misspelt `comproba`), `empieza el temporizador`, `empieza el cronómetro`, `pon un temporizador` (settimer; misspelt `empeza`), `hacia atrás` (backup), `color de tus ojos`, `tus ojos de color` (eyecolor), `tráeme el cubo` (fetchcube), `mírame a los ojos` (lookatme), `date la vuelta` (turnaround), `pon el volumen` (volumelevel), `graba un mensaje` (recordmessage), `jugar blackjack`, `a la veintiuna` (blackjack), `haz un caballito` (popawheelie): natural commands with no keyphrase or one under 0.3.
- Kept: `tarde` (goodmorning) no longer takes a natural sentence ("…la tarde es el mejor momento…" is 1 of 11 words); `no` and `correcto` are fixed by word start.

### Added in robot-wake T7 (2026-10-21)

Live, "Go to the charger." went to the LLM (`charger` covers 1 of 4 words) and the LLM said it had no command for it.
Full natural phrasings of the commands the tuning and first held-out sets lost were added (en: `go to the charger`,
`take a nap`, `bye bye`, `turn to the left`, `give me a fist bump`…; es: `ve a cargarte`, `vete al cargador`,
`gira hacia la izquierda`, `date una vuelta`, `hora de dormir`…), full phrases rather than single words so they do not
take non-commands. Single words were added only where a sentence of three words or less is a command and a longer one
is diluted under the 0.3 coverage (`bye`, `explore`, `blackjack`, `duerme`, `chao`). Statements that read like
commands were left out (`go to bed`, `a dormir`, `a la izquierda`, `da una vuelta`, `más fuerte`): by the 0.3 rule they
would take common sentences ("I go to bed at eleven", "la tienda está a la izquierda del banco"; not measured). The second held-out set was written after this
and measures it.
