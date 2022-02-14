# Uknow design

Have a server idiot. Replicate THAT. Not the fucking clients. Idiot.


Server has all the game logic. Just asks player to execute a command. 


Console - which is the "client" just executes commands based on what the gameserver decides. The gameserver will sometimes ask for player input to the console.




## Client and server timeouts and keep_alive connections

The reason most http servers in the wild, have a MaxIdleConnsPerHost > 0, is because they are
calling some upstream service while servicing one or more incoming requests in parallel. This means
if there 5 idle keep-alive conns present in the free list, we can use these to make 5 requests at
most. If the situation arises where to make more than 5 requests in parallel, new conns, i.e.
sockets have to be created to the host. Suppose that we have created 2 extra conns, so in total 7
conns are in use. After all of them return, only 5 will be kept (maybe in an MRU fashion) in the
free list and the rest will be closed and returned to the OS.

But in a synchronous app like uknow, you would usually and by that I mean 99% of the time, not make
multiple parallel requests to the same host. So it doesn't make sense to keep a large pool of idle
conns for any of the hosts.

Client organism

Maintains state regarding - whether it can respond or not.

Yes some commands can be approved by the client itself. Should we think about this on a command by command basis? What about a challenge?

That puts the admin in an executing-commands state. Every game-command is a state modifier. So it makes sense to route these through the admin.


## State machine

Starting from the state that all members have collected.

Server state machine

```mermaid
stateDiagram-v2
	[*] --> s0

	state "Adding new players" as s0

	state "Ready to serve cards" as s1

	state "Cards served" as s2

	state "Player chosen" as s3

	state "Wait for chosen player decision" as s4

	state "Received decision of chosen player" as s5

	state "Done syncing last player decision" as s6

	s0 --> s0: add new player

	s0 --> s1: set ready to serve cards

	s1 --> s2: serve cards and sync with players

	s2 --> s3: choose random player

	s3 --> s4: ask for decision from chosen player

	s4 --> s5: receive decision command from chosen player

	s5 --> s6: sync decision with each client

	s6 --> s3: choose next player

	s2 --> [*]
```

Client state machine

```mermaid
stateDiagram-v2

	state "Waiting to connect to admin" as s1
	state "Waiting for admin to serve cards" as s2
	state "Waiting for add new player messages" as s2
	state "Waiting for admin to choose player, after syncing local table" as s3


	[*] --> s1

	s1 --> s2: receive connect command from ui

	s2 --> s3: receive serve cards event

	s2 --> s2: receive add new player message

	s3 --> s4: wait for chosen player message

	s4 --> s51: we are chosen player

	s51 --> s6: ask player for decision and validate

	s6 --> s51: invalid input

	s6 --> s7: replicate command in local

	s7 --> s8: send sync message to admin and wait for ack

	s8 --> s3

	s51 --> s61: we are not chosen player

	s61 --> s71: wait for admin sync message

	s71 --> s81: replicate table state using sync message

	s81 --> s91: send replication ack to admin

	s91 --> s3
```

Starting approach

- PlayerClient handles POST /event.
- Creates a UI command based on the event and sends the command to ClientUI on a channel.