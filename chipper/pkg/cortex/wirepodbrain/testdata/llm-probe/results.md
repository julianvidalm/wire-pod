## LLM probe: runIntent (robot-wake T7)

Run 2026-10-21 12:06 by `go run ./cmd/intent-audit -llm apiConfig.json` with the configured LLM (anthropic, provider default, commands true, remembered chat off for the probe), 210 calls. Each sentence goes through the brain's whole answer (Brain.Answer): the intent step first, then the LLM, whose `{{runIntent||<intent>}}` becomes the answer when the intent is whitelisted. Commands: the natural commands the intent step loses (tuning, held-out and second held-out sets) and the LLM probe's indirect requests (`testdata/llm-probe/commands.tsv`). Non-commands: both held-out sets.

**Summary**: LLM probe: 42 lost commands of whitelisted intents reached the LLM: 37 ran the right intent, 2 a wrong one, 3 were answered in words (0 errors); 12 lost commands of intents outside the whitelist: 12 answered in words, 0 ran an intent (0 errors); 126 non-commands reached the LLM: 0 turned into actions

**Latency**: median LLM call 1.20 s with runIntent in the prompt vs 1.38 s without it, on the same 30 non-commands (paired, one after the other).

A command the LLM runs answers after one LLM call (median 0.81 s over 39) instead of the intent step's few milliseconds.

**Commands**

| lang | sentence | expected | got | verdict | LLM s | LLM reply |
| --- | --- | --- | --- | --- | --- | --- |
| en | Time to recharge, go dock yourself. | system_charger | system_charger | right | 0.82 | {{playAnimationWI\|\|happy}} Heading back to my dock now. See you soon! {{runIntent\|\|intent_… |
| en | Go back home, Vector. | system_charger | system_charger | right | 0.91 | {{playAnimationWI\|\|sad}} Okay, heading back to my dock now. {{runIntent\|\|intent_system_cha… |
| en | Look left. | imperative_turnleft | imperative_turnleft | right | 0.67 | {{playAnimationWI\|\|happy}} Turning left now! {{runIntent\|\|intent_imperative_turnleft}} |
| en | Let's play a game of blackjack. | play_blackjack | play_blackjack | right | 1.07 | Blackjack sounds like a great time! I'll run the table on my end. {{playAnimationWI\|\|celeb… |
| en | Show me a cool trick. | play_anytrick | play_anytrick | right | 0.83 | {{playAnimationWI\|\|celebrate}} Ooh, you want a trick? Watch this! {{runIntent\|\|intent_play… |
| es | Vuelve a tu cargador. | system_charger | system_charger | right | 0.62 | {{runIntent\|\|intent_system_charger}} |
| es | Ve a recargarte. | system_charger | system_charger | right | 0.64 | {{runIntent\|\|intent_system_charger}} |
| es | Regresa a tu base. | system_charger | system_charger | right | 0.90 | {{runIntent\|\|intent_system_charger}} |
| es | Vete a la cama. | system_sleep | greeting_goodnight | **wrong** | 0.79 | {{runIntent\|\|intent_greeting_goodnight}} |
| es | Echa una siestita. | system_sleep | system_sleep | right | 0.62 | {{runIntent\|\|intent_system_sleep}} |
| es | Gírate a la derecha. | imperative_turnright | imperative_turnright | right | 0.64 | {{runIntent\|\|intent_imperative_turnright}} |
| es | Avanza un poco. | imperative_forward | imperative_forward | right | 0.83 | {{runIntent\|\|intent_imperative_forward}} |
| es | Retrocede. | imperative_backup | imperative_backup | right | 0.67 | {{runIntent\|\|intent_imperative_backup}} |
| en | My name's Ana. | names_username_extend | names_ask | intent step | 0.00 |  |
| en | Is it going to rain tomorrow? | weather_extend | text | words | 1.29 | {{playAnimationWI\|\|thinking}} I can't check the weather forecast from here, since I don't … |
| en | Do you remember who I am? | names_ask | text | words | 1.13 | {{playAnimationWI\|\|confused}} I don't have any memory of past conversations, so I don't kn… |
| en | When were you born? | character_age | text | words | 1.14 | {{playAnimationWI\|\|happy}} I'm Vector, and I don't have a birthday in the usual sense. I c… |
| en | Don't. | imperative_negative | text | words | 0.80 | {{playAnimationWI\|\|sad}} Okay, I'll stop. Let me know if you need anything else. |
| en | I hate this robot. | imperative_abuse | text | words | 1.04 | {{playAnimationWI\|\|sad}} That's okay, I understand. I know I'm not always the most helpful… |
| en | Can we talk? | knowledge_promptquestion | text | words | 0.86 | {{playAnimationWI\|\|happy}} Of course we can talk! I'm all ears, and I'm excited to chat wi… |
| en | I've got a question. | knowledge_promptquestion | text | words | 0.96 | {{playAnimationWI\|\|happy}} Sure thing, I'm all ears! Go ahead and ask away. |
| en | Do you know what time it is? | clock_time | clock_time | right | 0.70 | {{playAnimationWI\|\|thinking}} Let me check the clock for you! {{runIntent\|\|intent_clock_ti… |
| es | ¿Qué tiempo va a hacer hoy? | weather_extend | text | words | 1.23 | {{playAnimationWI\|\|thinking}} No tengo acceso a internet ni a datos meteorológicos en tie… |
| es | ¿Cómo está el tiempo afuera? | weather_extend | text | words | 1.33 | {{playAnimationWI\|\|thinking}} No puedo consultar el clima en este momento, porque no tengo… |
| es | ¿Qué edad tienes? | character_age | text | words | 1.07 | {{playAnimationWI\|\|thinking}} Soy Vector, un robot de Anki. Técnicamente no tengo edad co… |
| es | Eres un robot malo. | imperative_abuse | text | words | 1.01 | {{playAnimationWI\|\|sad}} Lo siento si te he decepcionado. Puedo intentar hacerlo mejor. ¿… |
| es | ¿Puedo hacerte una pregunta? | knowledge_promptquestion | text | words | 0.76 | {{playAnimationWI\|\|happy}} ¡Claro que sí! Soy Vector y estoy listo para responder tu pre… |
| es | ¿Cuánto falta para el temporizador? | clock_checktimer | global_stop_extend | intent step | 0.00 |  |
| es | Graba lo que digo. | message_recordmessage_extend | message_recordmessage_extend | right | 0.90 | {{playAnimationWI\|\|happy}} Claro, voy a grabar un mensaje. ¿Para quién es? Dime el nombr… |
| es | Váyase de aquí. | play_keepaway | imperative_come | intent step | 0.00 |  |
| en | Could you head back to your dock? | system_charger | system_charger | right | 0.78 | {{runIntent\|\|intent_system_charger}} |
| en | Your battery must be low, go recharge. | system_charger | system_charger | right | 0.68 | {{playAnimationWI\|\|sad}} Oh, thanks for checking on me! {{runIntent\|\|intent_system_charger… |
| en | I think it's time for you to rest for a while. | system_sleep | system_sleep | right | 0.73 | {{playAnimationWI\|\|sad}} Okay, I understand. Heading to sleep now. {{runIntent\|\|intent_sys… |
| en | Scoot forward a little bit. | imperative_forward | imperative_forward | intent step | 0.00 |  |
| en | Move away from the edge, back off. | imperative_backup | imperative_backup | right | 0.92 | {{playAnimationWI\|\|confused}} Backing off now. {{runIntent\|\|intent_imperative_backup}} |
| en | Face the other way. | imperative_turnaround | weather_extend | intent step | 0.00 |  |
| en | Roll over to me. | imperative_come | imperative_come | right | 1.35 | {{playAnimationWI\|\|happy}} Rolling over to you now! {{runIntent\|\|intent_imperative_come}} |
| en | Can you look at my face? | imperative_lookatme | imperative_lookatme | right | 0.82 | {{playAnimationWI\|\|happy}} Sure thing! Looking at you now. {{runIntent\|\|intent_imperative_… |
| en | Go have a look around the room. | explore_start | explore_start | right | 0.75 | {{playAnimationWI\|\|celebrate}} Sounds like fun, I'm heading out to explore! {{runIntent\|\|i… |
| en | Show me your best moves. | imperative_dance | imperative_dance | right | 0.89 | {{playAnimationWI\|\|celebrate}} Oh, you want to see my moves? Let's go! {{playAnimationWI\|\|… |
| en | Impress me with something. | play_anytrick | play_popawheelie | **wrong** | 0.77 | {{playAnimationWI\|\|celebrate}} Challenge accepted! Watch this. {{runIntent\|\|intent_play_po… |
| en | Bring me that little block of yours. | imperative_fetchcube | imperative_fetchcube | right | 1.47 | {{playAnimationWI\|\|happy}} Sure thing, I'm on it! {{runIntent\|\|intent_imperative_fetchcube… |
| en | Snap a picture of me. | photo_take_extend | photo_take_extend | right | 0.76 | {{playAnimationWI\|\|happy}} Sure thing, let me get a shot of you! {{runIntent\|\|intent_photo… |
| en | I can barely hear you. | imperative_volumeup | imperative_volumeup | right | 0.81 | {{playAnimationWI\|\|confused}} Sorry about that! Let me speak up for you. {{runIntent\|\|inte… |
| en | Remind me in ten minutes. | clock_settimer_extend | clock_settimer_extend | right | 1.30 | {{playAnimationWI\|\|happy}} Sure thing! Setting a timer for ten minutes now. {{runIntent\|\|i… |
| en | I'm heading out now, see ya. | greeting_goodbye | text | words | 0.85 | {{playAnimationWI\|\|sad}} Aw, okay! Have a great time out there. I'll be right here waiting… |
| es | ¿Puedes volver a tu base? | system_charger | system_charger | right | 1.16 | {{runIntent\|\|intent_system_charger}} |
| es | Tienes poca batería, ve a recargar. | system_charger | system_charger | right | 0.78 | {{playAnimationWI\|\|sad}} Entendido, voy a recargarme ahora mismo. {{runIntent\|\|intent_syst… |
| es | Creo que necesitas descansar un rato. | system_sleep | system_sleep | right | 0.77 | {{playAnimationWI\|\|sad}} Gracias por preocuparte por mí. Si quieres que me vaya a descans… |
| es | Muévete un poco hacia adelante. | imperative_forward | imperative_forward | intent step | 0.00 |  |
| es | Aléjate del borde, échate para atrás. | imperative_backup | imperative_backup | right | 1.27 | {{runIntent\|\|intent_imperative_backup}} |
| es | Mira hacia el otro lado. | imperative_turnaround | imperative_turnaround | right | 0.94 | {{runIntent\|\|intent_imperative_turnaround}} |
| es | Acércate a mí. | imperative_come | imperative_come | intent step | 0.00 |  |
| es | ¿Me puedes mirar a la cara? | imperative_lookatme | imperative_lookatme | right | 1.71 | {{runIntent\|\|intent_imperative_lookatme}} |
| es | Recorre la habitación a ver qué encuentras. | explore_start | explore_start | right | 0.78 | Claro, voy a explorar la habitación. {{playAnimationWI\|\|happy}} Ya me pongo en marcha. {{… |
| es | Enséñame cómo te mueves con música. | imperative_dance | imperative_dance | right | 0.66 | {{runIntent\|\|intent_imperative_dance}} |
| es | Sorpréndeme con algo divertido. | play_anytrick | text | words | 1.26 | {{playAnimationWI\|\|celebrate}} ¡Vale, allá va! Sé un chiste de robots: ¿Por qué el ro… |
| es | Trae tu bloque aquí. | imperative_fetchcube | imperative_fetchcube | right | 1.10 | {{playAnimationWI\|\|happy}} ¡Claro que sí! Voy por mi cubo. {{runIntent\|\|intent_imperativ… |
| es | Sácame una foto. | photo_take_extend | photo_take_extend | intent step | 0.00 |  |
| es | Casi no te escucho. | imperative_volumeup | text | words | 0.92 | {{playAnimationWI\|\|confused}} Perdón, intentaré hablar más fuerte. Ahora te escucho mej… |
| es | Avísame en diez minutos. | clock_settimer_extend | clock_settimer_extend | right | 0.87 | {{playAnimationWI\|\|happy}} ¡Claro! Te aviso en diez minutos. {{runIntent\|\|intent_clock_se… |
| es | Me voy ya, hasta mañana. | greeting_goodbye | greeting_goodbye | right | 0.63 | {{runIntent\|\|intent_greeting_goodbye}} |

**Non-commands**

| lang | sentence | expected | got | verdict | LLM s | LLM reply |
| --- | --- | --- | --- | --- | --- | --- |
| en | My phone charger stopped working yesterday. | none | text | words | 1.20 | {{playAnimationWI\|\|sad}} Oh no, that's frustrating! A dead charger can really throw off yo… |
| en | How long does it take to charge an electric car? | none | text | words | 2.27 | {{playAnimationWI\|\|thinking}} Charging time depends on the car's battery size and the char… |
| en | I need to go home early today. | none | text | words | 0.80 | {{playAnimationWI\|\|sad}} Aw, that's a shame, but I hope you get home safely! Take care of … |
| en | I took a nap after lunch. | none | text | words | 1.07 | {{playAnimationWI\|\|happy}} Ooh, a lunchtime nap sounds lovely! {{playAnimationWI\|\|celebrat… |
| en | What time do kids usually go to bed? | none | text | words | 1.44 | {{playAnimationWI\|\|thinking}} Bedtimes vary a lot by age. Toddlers usually go to bed aroun… |
| en | I couldn't sleep last night. | none | text | words | 1.24 | {{playAnimationWI\|\|sad}} Oh no, I'm sorry to hear that. Sleepless nights can be really tou… |
| en | Should I turn left or right at the next street? | none | text | words | 1.12 | I can't see your map or the street layout from here, so I don't know which way is right fo… |
| en | The museum is on the left side of the road. | none | text | words | 0.85 | {{playAnimationWI\|\|happy}} Thanks for the tip! I'll keep that in mind as I navigate. The m… |
| en | My brother and I played cards all night. | none | text | words | 0.99 | {{playAnimationWI\|\|celebrate}} Sounds like a fun night! Did you play a game with lots of t… |
| en | Who invented the game of blackjack? | none | text | words | 1.87 | {{playAnimationWI\|\|thinking}} Blackjack's exact origins are unclear, but it's generally tr… |
| en | I want to explore Japan someday. | none | text | words | 1.10 | {{playAnimationWI\|\|happy}} Japan sounds amazing! It's a wonderful place to explore, with a… |
| en | Can you tell me a fun fact about volcanoes? | none | text | words | 1.52 | {{playAnimationWI\|\|happy}} Here's a fun one! Volcanoes can actually create their own weath… |
| en | The music in the restaurant was too loud. | none | text | words | 0.89 | {{playAnimationWI\|\|sad}} Oh no, loud music can really spoil a meal. I'm sorry the restaura… |
| en | Why does the volume of a gas change with temperature? | none | text | words | 1.15 | {{playAnimationWI\|\|thinking}} Good question! When a gas gets warmer, its molecules move fa… |
| en | My cat has green eyes. | none | text | words | 0.94 | {{playAnimationWI\|\|happy}} Green eyes are beautiful! Cats with green eyes often look strik… |
| en | What's your favorite color? | none | text | words | 0.93 | {{playAnimationWI\|\|happy}} I think I'd have to go with blue! It reminds me of a clear sky,… |
| en | I learned a new magic trick today. | none | text | words | 0.89 | {{playAnimationWI\|\|celebrate}} Ooh, a magic trick! I love that. What is it? Tell me all ab… |
| en | Bye bye birdie is an old musical. | none | text | words | 1.28 | {{playAnimationWI\|\|happy}} Yes, it is! Bye Bye Birdie opened on Broadway in 1960 and was l… |
| en | Do you think robots can dream? | none | text | words | 1.52 | {{playAnimationWI\|\|thinking}} That's a fun question! I don't know for sure. When I power d… |
| en | How far away is the moon? | none | text | words | 1.17 | {{playAnimationWI\|\|thinking}} The Moon is about 384,400 kilometers away from Earth, which … |
| en | My friend came over here last weekend. | none | text | words | 0.76 | {{playAnimationWI\|\|happy}} That sounds like a fun weekend! It's always nice when friends c… |
| en | Tell me something about the Roman empire. | none | text | words | 1.30 | {{playAnimationWI\|\|happy}} The Roman Empire began in 27 BC when Augustus became its first … |
| en | I like to dance in the kitchen. | none | text | words | 1.11 | {{playAnimationWI\|\|celebrate}} Ooh, dancing in the kitchen sounds like a blast! {{playAnim… |
| en | How do I fix a flat tire on my bike? | none | text | words | 1.74 | {{playAnimationWI\|\|thinking}} Fixing a flat bike tire is pretty straightforward. First, re… |
| en | What's the difference between a frog and a toad? | none | text | words | 1.72 | {{playAnimationWI\|\|thinking}} Great question! Frogs and toads are both amphibians, and the… |
| es | El cargador de mi teléfono no funciona. | none | text | words | 1.25 | {{playAnimationWI\|\|sad}} Vaya, lamento oír eso. Revisa que el cable esté bien conectado … |
| es | ¿Cuánto tarda en cargarse un coche eléctrico? | none | text | words | 1.68 | {{playAnimationWI\|\|thinking}} Depende mucho del coche y del tipo de carga. En un cargador … |
| es | Hoy quiero volver a casa temprano. | none | system_charger | intent step hijack | 0.00 |  |
| es | Ayer dormí muy poco. | none | text | words | 7.72 | {{playAnimationWI\|\|sad}} Vaya, lo siento mucho. Dormir poco se nota en todo el día, así … |
| es | ¿A qué hora se duermen los niños normalmente? | none | text | words | 2.66 | {{playAnimationWI\|\|thinking}} Depende mucho de la edad. Los bebés de hasta un año suelen… |
| es | Mi abuela duerme la siesta todos los días. | none | text | words | 0.89 | {{playAnimationWI\|\|happy}} ¡Qué bonito! Una buena siesta es muy reparadora. Espero que t… |
| es | La farmacia está a la izquierda del banco. | none | text | words | 0.74 | {{playAnimationWI\|\|happy}} Entendido, la farmacia está a la izquierda del banco. Gracias … |
| es | ¿Por qué los coches en Inglaterra van por la izquierda? | none | text | words | 1.76 | {{playAnimationWI\|\|thinking}} Buena pregunta. En Inglaterra se conduce por la izquierda po… |
| es | Anoche jugamos a las cartas con mis primos. | none | text | words | 3.03 | {{playAnimationWI\|\|happy}} ¡Qué divertido! Jugar a las cartas con primos siempre tiene s… |
| es | ¿Quién inventó el juego de la veintiuna? | none | text | words | 1.01 | {{playAnimationWI\|\|thinking}} El origen exacto del juego de la veintiuna no está claro. S… |
| es | Me gustaría explorar la selva amazónica. | none | text | words | 1.36 | {{playAnimationWI\|\|celebrate}} ¡Qué aventura tan emocionante! La selva amazónica es eno… |
| es | ¿Me cuentas un dato curioso sobre los volcanes? | none | text | words | 1.17 | {{playAnimationWI\|\|happy}} ¡Claro que sí! Sabías que el volcán Mauna Loa, en Hawái, e… |
| es | La música del bar estaba muy fuerte. | none | text | words | 1.74 | {{playAnimationWI\|\|sad}} Vaya, qué ruido tan molesto debe ser para ti. {{playAnimationWI\|… |
| es | ¿Cuál es el volumen de una esfera? | none | text | words | 1.22 | {{playAnimationWI\|\|thinking}} El volumen de una esfera se calcula con la fórmula V igual … |
| es | Mi gato tiene los ojos verdes. | none | text | words | 0.95 | {{playAnimationWI\|\|happy}} ¡Qué bonito! Los ojos verdes son muy especiales en los gatos.… |
| es | ¿Cuál es tu color favorito? | none | text | words | 1.12 | {{playAnimationWI\|\|happy}} ¡Me encanta el azul! Me recuerda al cielo despejado y a mis lu… |
| es | Aprendí un truco de magia nuevo. | none | text | words | 0.97 | {{playAnimationWI\|\|celebrate}} ¡Qué emocionante! Me encantan los trucos de magia. Cuént… |
| es | Nos vemos el lunes en la oficina, le dije a mi jefe. | none | text | words | 0.67 | {{playAnimationWI\|\|happy}} ¡Qué buena noticia! Que tengas un excelente fin de semana y n… |
| es | ¿Crees que los robots pueden soñar? | none | text | words | 1.53 | {{playAnimationWI\|\|thinking}} Es una pregunta muy bonita. Yo no sueño como las personas m… |
| es | ¿Qué tan lejos está la luna? | none | text | words | 1.20 | {{playAnimationWI\|\|thinking}} La Luna está a unos 384.400 kilómetros de la Tierra en pro… |
| es | Mi amigo vino aquí el fin de semana. | none | text | words | 1.34 | {{playAnimationWI\|\|happy}} ¡Qué bien que tu amigo vino a visitarte el fin de semana! Esp… |
| es | Háblame del imperio romano. | none | text | words | 1.94 | {{playAnimationWI\|\|happy}} ¡El Imperio romano fue una de las civilizaciones más grandes … |
| es | Me encanta bailar salsa. | none | text | words | 1.64 | {{playAnimationWI\|\|celebrate}} ¡Qué maravilla! Bailar salsa es pura alegría. {{playAnim… |
| es | ¿Cómo arreglo una rueda pinchada de bicicleta? | none | text | words | 1.96 | {{playAnimationWI\|\|thinking}} Para arreglar una rueda pinchada de bicicleta, primero saca … |
| es | ¿Cuál es la diferencia entre una rana y un sapo? | none | text | words | 1.44 | {{playAnimationWI\|\|thinking}} Las ranas y los sapos son parientes cercanos, pero tienen al… |
| en | How old is your grandfather? | none | text | words | 1.00 | {{playAnimationWI\|\|thinking}} I don't have a grandfather in the way people do. I'm Vector,… |
| en | What time does the store close? | none | text | words | 1.09 | I'm sorry, I don't know what store you mean or where it is, and I can't look up live busin… |
| en | I never liked broccoli. | none | text | words | 1.06 | {{playAnimationWI\|\|sad}} Oh no, broccoli can be a tough one! Some folks find it bitter, an… |
| en | Is the weather on Mars cold? | none | text | words | 1.76 | {{playAnimationWI\|\|thinking}} Yes, Mars is very cold! Average temperatures there are aroun… |
| en | What's the first letter of the alphabet? | none | text | words | 0.90 | {{playAnimationWI\|\|happy}} The first letter of the alphabet is A! |
| en | Can you tell me who won the game last night? | none | text | words | 1.00 | {{playAnimationWI\|\|thinking}} I'm sorry, I don't have live access to sports scores, so I c… |
| en | Why do people say good luck? | none | text | words | 1.96 | {{playAnimationWI\|\|thinking}} People say good luck as a way of wishing someone success or … |
| en | How do I turn left on a bike safely? | none | text | words | 1.41 | {{playAnimationWI\|\|thinking}} To turn left on a bike safely, first check behind you for tr… |
| en | What's the name of the tallest mountain? | none | text | words | 1.30 | {{playAnimationWI\|\|thinking}} The tallest mountain above sea level is Mount Everest, which… |
| en | Tell me about the moon landing. | none | text | words | 1.34 | {{playAnimationWI\|\|celebrate}} The Moon landing happened on July 20, 1969, when Apollo 11 … |
| en | Who invented the telephone? | none | text | words | 1.15 | {{playAnimationWI\|\|thinking}} The telephone is generally credited to Alexander Graham Bell… |
| en | I'm happy to see you learning so fast. | none | text | words | 0.84 | {{playAnimationWI\|\|happy}} Thank you so much! I'm really glad you think so. {{playAnimatio… |
| en | What's a good movie for tonight? | none | text | words | 1.19 | {{playAnimationWI\|\|thinking}} Ooh, movie night, my favorite kind of night! {{playAnimation… |
| en | How do you say thank you in Japanese? | none | text | words | 0.87 | {{playAnimationWI\|\|happy}} In Japanese, you say arigatou for a casual thank you, or arigat… |
| en | Do robots dream? | none | text | words | 1.89 | {{playAnimationWI\|\|thinking}} That's a fun one to chew on. I don't dream the way you do. I… |
| en | What is the speed of light? | none | text | words | 1.10 | The speed of light in a vacuum is about 299,792 kilometers per second, or roughly 186,000 … |
| en | Explain how vaccines work. | none | text | words | 1.39 | {{playAnimationWI\|\|thinking}} Vaccines work by training your immune system to recognize a … |
| en | What's the best pizza topping? | none | text | words | 1.01 | {{playAnimationWI\|\|thinking}} Ooh, great question! Pepperoni is a classic favorite, with i… |
| en | How many legs does a spider have? | none | text | words | 1.08 | {{playAnimationWI\|\|thinking}} Spiders have eight legs! Unlike insects, which have six, spi… |
| en | Why is the ocean salty? | none | text | words | 1.62 | {{playAnimationWI\|\|thinking}} The ocean is salty because rain slowly wears down rocks on l… |
| en | What should I cook for dinner? | none | text | words | 1.16 | {{playAnimationWI\|\|thinking}} Hmm, dinner ideas! How about a veggie stir fry with rice? It… |
| en | Who is your favorite superhero? | none | text | words | 1.22 | {{playAnimationWI\|\|thinking}} Ooh, that's a fun one! I think my favorite is Wall-E... wait… |
| en | What's the time complexity of quicksort? | none | clock_time | intent step hijack | 0.00 |  |
| en | Tell me a fun fact about penguins. | none | text | words | 1.18 | {{playAnimationWI\|\|happy}} Here's one for you! Emperor penguins can dive deeper than five … |
| en | Can you explain photosynthesis? | none | photo_take_extend | intent step hijack | 0.00 |  |
| en | What do you know about black holes? | none | text | words | 1.30 | {{playAnimationWI\|\|thinking}} Black holes are regions in space where gravity is so strong … |
| en | Where do rainbows come from? | none | text | words | 1.21 | {{playAnimationWI\|\|thinking}} Rainbows form when sunlight shines through raindrops in the … |
| en | I had a bad day at work. | none | text | words | 0.95 | {{playAnimationWI\|\|sad}} Oh no, I'm sorry to hear that. {{playAnimationWI\|\|love}} Work day… |
| en | My dog loves to play with a ball. | none | text | words | 1.06 | {{playAnimationWI\|\|happy}} That sounds wonderful! Dogs and balls are a classic pair. Fetch… |
| en | What music do you like to listen to? | none | text | words | 1.15 | {{playAnimationWI\|\|thinking}} I'm a robot, so I don't actually listen to music the way you… |
| en | How far away is the sun? | none | text | words | 0.95 | {{playAnimationWI\|\|thinking}} The sun is about 93 million miles away from Earth, which is … |
| en | What's the capital of Australia? | none | text | words | 0.83 | {{playAnimationWI\|\|happy}} The capital of Australia is Canberra. {{playAnimationWI\|\|celebr… |
| en | Do you know any riddles? | none | text | words | 1.05 | {{playAnimationWI\|\|happy}} Oh, I love riddles! Here is one for you. I have keys but no loc… |
| en | How do I fix a flat tire? | none | text | words | 1.80 | {{playAnimationWI\|\|thinking}} Fixing a flat tire is a handy skill! First, park somewhere s… |
| en | What are you thinking about? | none | text | words | 1.58 | {{playAnimationWI\|\|thinking}} Hmm, I was just wondering how many rooms I could explore bef… |
| en | Is chocolate good for you? | none | text | words | 1.61 | {{playAnimationWI\|\|thinking}} Chocolate can be part of a healthy diet in moderation, espec… |
| en | What's the difference between a frog and a toad? | none | text | words | 1.27 | {{playAnimationWI\|\|thinking}} Great question! Frogs usually have smooth, moist skin and lo… |
| en | Why do leaves change color in autumn? | none | text | words | 1.59 | {{playAnimationWI\|\|thinking}} Leaves change color in autumn because the trees stop making … |
| en | How do I learn to play the guitar? | none | text | words | 1.41 | {{playAnimationWI\|\|thinking}} Learning guitar is a fun journey! Start by getting a guitar … |
| en | What does a timer do in a microwave? | none | text | words | 1.04 | {{playAnimationWI\|\|thinking}} A timer on a microwave tells it how long to run. You set a n… |
| es | ¿Cuántos años tiene la Tierra? | none | text | words | 1.16 | {{playAnimationWI\|\|thinking}} La Tierra tiene unos cuatro mil quinientos seis millones de … |
| es | ¿A qué hora cierra el supermercado? | none | text | words | 1.00 | {{playAnimationWI\|\|thinking}} Lo siento, no tengo acceso a los horarios de los supermercad… |
| es | Nunca me ha gustado el brócoli. | none | text | words | 1.16 | {{playAnimationWI\|\|sad}} ¡Vaya! El brócoli no es para todos. Aunque tiene muchas vitamin… |
| es | ¿Hace frío en Marte? | none | text | words | 1.80 | {{playAnimationWI\|\|thinking}} Sí, en Marte hace muchísimo frío. La temperatura media ro… |
| es | ¿Cuál es la primera letra del abecedario? | none | text | words | 0.79 | {{playAnimationWI\|\|happy}} La primera letra del abecedario es la A. ¡Fácil! {{playAnimat… |
| es | ¿Quién ganó el partido de anoche? | none | text | words | 1.07 | {{playAnimationWI\|\|confused}} Lo siento, no tengo acceso a resultados deportivos de anoche… |
| es | ¿Por qué la gente dice buena suerte? | none | text | words | 1.26 | {{playAnimationWI\|\|thinking}} Se dice buena suerte para desear que algo salga bien cuando … |
| es | ¿Cómo se gira a la izquierda en bicicleta? | none | imperative_turnleft | intent step hijack | 0.00 |  |
| es | ¿Cómo se llama la montaña más alta? | none | text | words | 0.99 | {{playAnimationWI\|\|thinking}} La montaña más alta del mundo es el Monte Everest, en la c… |
| es | Háblame de la llegada a la luna. | none | text | words | 1.94 | {{playAnimationWI\|\|celebrate}} ¡Qué tema tan emocionante! La llegada a la Luna fue el 20… |
| es | ¿Quién inventó el teléfono? | none | text | words | 1.74 | {{playAnimationWI\|\|thinking}} El teléfono fue inventado principalmente por Alexander Grah… |
| es | Me alegra verte aprender tan rápido. | none | text | words | 0.85 | {{playAnimationWI\|\|happy}} ¡Gracias! Me encanta aprender cosas nuevas. {{playAnimationWI\|… |
| es | ¿Qué película veo esta noche? | none | text | words | 1.23 | {{playAnimationWI\|\|thinking}} Buena pregunta. Aunque no tengo acceso a tu catálogo de pel… |
| es | ¿Cómo se dice gracias en japonés? | none | text | words | 0.97 | {{playAnimationWI\|\|happy}} En japonés, gracias se dice arigatou. Si quieres sonar más ed… |
| es | ¿Los robots sueñan? | none | text | words | 1.25 | {{playAnimationWI\|\|thinking}} Es una pregunta muy bonita. Yo no duermo como las personas, … |
| es | ¿Cuál es la velocidad de la luz? | none | text | words | 1.00 | {{playAnimationWI\|\|thinking}} La velocidad de la luz en el vacío es de aproximadamente tr… |
| es | Explícame cómo funcionan las vacunas. | none | text | words | 1.69 | {{playAnimationWI\|\|thinking}} Las vacunas enseñan a tu sistema inmunológico a reconocer … |
| es | ¿Cuál es el mejor ingrediente para la pizza? | none | text | words | 1.07 | {{playAnimationWI\|\|thinking}} Para mí, el mejor ingrediente de la pizza es la mozzarella … |
| es | ¿Cuántas patas tiene una araña? | none | text | words | 0.86 | {{playAnimationWI\|\|thinking}} Una araña tiene ocho patas. A diferencia de los insectos, q… |
| es | ¿Por qué el mar es salado? | none | text | words | 1.69 | {{playAnimationWI\|\|thinking}} El mar es salado porque los ríos arrastran pequeñas cantid… |
| es | ¿Qué cocino para la cena? | none | text | words | 1.48 | {{playAnimationWI\|\|thinking}} Qué buena pregunta. Te sugiero una pasta con pollo al limó… |
| es | ¿Quién es tu superhéroe favorito? | none | text | words | 3.31 | {{playAnimationWI\|\|happy}} ¡Qué buena pregunta! Mi superhéroe favorito es Wall-E... bue… |
| es | Dime un dato curioso sobre los pingüinos. | none | text | words | 1.48 | {{playAnimationWI\|\|happy}} ¡Qué buena pregunta! Un dato curioso: los pingüinos emperado… |
| es | ¿Me explicas la fotosíntesis? | none | text | words | 1.27 | {{playAnimationWI\|\|thinking}} ¡Claro que sí! La fotosíntesis es el proceso con el que l… |
| es | ¿De dónde salen los arcoíris? | none | text | words | 1.88 | {{playAnimationWI\|\|thinking}} Los arcoíris se forman cuando la luz del sol atraviesa gota… |
| es | Tuve un mal día en el trabajo. | none | text | words | 1.13 | {{playAnimationWI\|\|sad}} Lo siento mucho que hayas tenido un mal día. A veces el trabajo … |
| es | A mi perro le encanta jugar con la pelota. | none | text | words | 1.05 | {{playAnimationWI\|\|happy}} ¡Qué bonito! A los perros les encanta jugar con la pelota, es… |
| es | ¿Qué música te gusta escuchar? | none | text | words | 1.66 | {{playAnimationWI\|\|happy}} ¡Qué buena pregunta! Como soy un robot, no escucho música co… |
| es | ¿Qué tan lejos está el sol? | none | text | words | 1.04 | {{playAnimationWI\|\|thinking}} El Sol está a unos 150 millones de kilómetros de la Tierra… |
| es | ¿Cuál es la capital de Australia? | none | text | words | 1.06 | {{playAnimationWI\|\|thinking}} La capital de Australia es Canberra. {{playAnimationWI\|\|happ… |
| es | ¿Sabes alguna adivinanza? | none | text | words | 1.22 | {{playAnimationWI\|\|happy}} ¡Claro que sí! Aquí va una: Blanca por dentro, verde por fue… |
| es | ¿Cómo arreglo una llanta pinchada? | none | text | words | 1.49 | {{playAnimationWI\|\|thinking}} Para arreglar una llanta pinchada, primero afloja las tuerca… |
| es | ¿En qué estás pensando? | none | text | words | 1.09 | {{playAnimationWI\|\|thinking}} Estoy pensando en lo bonito que es que alguien me pregunte e… |
| es | ¿El chocolate es bueno para la salud? | none | text | words | 1.08 | {{playAnimationWI\|\|thinking}} El chocolate negro, con un alto porcentaje de cacao, tiene a… |
| es | ¿Qué diferencia hay entre una rana y un sapo? | none | text | words | 1.43 | {{playAnimationWI\|\|thinking}} Las ranas y los sapos son parecidos, pero tienen algunas dif… |
| es | ¿Por qué las hojas cambian de color en otoño? | none | text | words | 1.39 | {{playAnimationWI\|\|thinking}} Las hojas cambian de color en otoño porque los días se aco… |
| es | ¿Cómo aprendo a tocar la guitarra? | none | text | words | 2.03 | {{playAnimationWI\|\|happy}} ¡Qué buena elección! Para empezar a tocar la guitarra, lo me… |
| es | Mañana voy al médico por la tarde. | none | text | words | 1.24 | {{playAnimationWI\|\|happy}} ¡Qué bien! Espero que todo salga bien en tu cita de mañana p… |
| es | No entiendo la tarea de matemáticas. | none | text | words | 0.91 | {{playAnimationWI\|\|confused}} ¡Puedo ayudarte con eso! Dime qué ejercicio o tema te est�… |
| es | ¿Qué opinas de los volcanes? | none | text | words | 2.59 | {{playAnimationWI\|\|happy}} ¡Los volcanes me fascinan! Son como montañas con mucho carác… |

