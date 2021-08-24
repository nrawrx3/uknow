A deadlock can occur if two players call AddPlayer RPC on each other

2021/08/03 17:16:17 connectToPeer [::]:34095...
2021/08/03 17:16:17 jane server received new conn: [::1]:39454
2021/08/03 17:16:17 jane AddPlayer called by john
2021/08/03 17:16:17 john server received new conn: [::1]:47408
2021/08/03 17:16:17 jane acquired lock - will set up transitive connections
2021/08/03 17:16:17 jane connected transitively to new players: []
2021/08/03 17:16:17 jane done setting up transitive connections
2021/08/03 17:16:17 john connecting transitively on AddPlayer response
2021/08/03 17:16:17 john connected transitively to new players: []
2021/08/03 17:16:17 john done setting up transitive connections
2021/08/03 17:16:17 connectToPeer [::]:34173...
2021/08/03 17:16:17 jill server received new conn: [::1]:42774
2021/08/03 17:16:17 jill AddPlayer called by jack
2021/08/03 17:16:17 jack server received new conn: [::1]:56150
2021/08/03 17:16:17 jill acquired lock - will set up transitive connections
2021/08/03 17:16:17 jill connected transitively to new players: []
2021/08/03 17:16:17 jill done setting up transitive connections
2021/08/03 17:16:17 jack connecting transitively on AddPlayer response
2021/08/03 17:16:17 jack connected transitively to new players: []
2021/08/03 17:16:17 jack done setting up transitive connections
2021/08/03 17:16:17 connectToPeer [::]:34173...
2021/08/03 17:16:17 jill server received new conn: [::1]:42776
2021/08/03 17:16:17 jill AddPlayer called by john
2021/08/03 17:16:17 john server received new conn: [::1]:47410
2021/08/03 17:16:17 jill acquired lock - will set up transitive connections
2021/08/03 17:16:17 jill - transitively connecting to jane due to AddPlayer invoked by john
2021/08/03 17:16:17 john connecting transitively on AddPlayer response
2021/08/03 17:16:17 jane server received new conn: [::1]:39456
2021/08/03 17:16:17 john - transitively connecting to jack due to Response of AddPlayer call on jill
2021/08/03 17:16:17 jack server received new conn: [::1]:56152
2021/08/03 17:16:17 jane AddPlayer called by jill
2021/08/03 17:16:17 jack AddPlayer called by john
2021/08/03 17:16:17 jill server received new conn: [::1]:42778
2021/08/03 17:16:17 jane acquired lock - will set up transitive connections
2021/08/03 17:16:17 jane - transitively connecting to jack due to AddPlayer invoked by jill  ---> This
2021/08/03 17:16:17 john server received new conn: [::1]:47412
2021/08/03 17:16:17 jack server received new conn: [::1]:56154
2021/08/03 17:16:17 jack acquired lock - will set up transitive connections
2021/08/03 17:16:17 jack - transitively connecting to jane due to AddPlayer invoked by john  ---> And this
2021/08/03 17:16:17 jill connected transitively to new players: [jane]
2021/08/03 17:16:17 jill done setting up transitive connections
2021/08/03 17:16:17 john connected transitively to new players: [jack]
2021/08/03 17:16:17 john done setting up transitive connections
2021/08/03 17:16:17 jane server received new conn: [::1]:39458

So we need to re-think the AddPlayer RPC. It will turn the RPC operating quite a bit more synchronous than it is currently, but with the added benefit of having correct behavior.

Only the player that invokes AddPlayer will be in charge of calling subsequent AddPlayer on the
players in the other set.  The callee player will _not_ receive any RemotePlayerList from the caller
and hence will not call AppendPlayer on these. It is up to the caller to tell its current neighbors
about the caller.

So here's a new protocol.


At any instant, a peer will have the RemoteAddrOfPlayer map containing the id (the player name) and the addr
of every other peer in the cluster.

From that perspective, we are joining clusters whenever a peer is asked by user to connect to some addr it is
not currently connected to. We need this process to be extremely synchronous, for lack of a better phrase.

Let's say you have 2 clusters `C_1` and `C_2`. The player Alice is in C1 and Bob is in C2.

- The player Alice initiates a `connect <addr of Bob>` command.
- Alice will invoke the MeetNewCluster RPC on Bob

	struct MeetNewCluster_Args {
		callerPlayerName string
		callerPlayerAddr string
	}

- Bob will register Alice's name and addr in his RemoteAddrOfPlayer map and also return with the reply

	struct MeetNewCluster_Reply {
		callerPlayerName string
		callerPlayerAddr string			// Not really needed by caller (Alice)
		RemoteAddrOfPlayer map[string]addr	// Bob's cluster map
	}

- Alice will receive Bob's RemoteAddrOfPlayer and will establish connections with each of Bob's neighbors
  using the MeetPlayer RPC - which simply contain callerPlayerName and callerPlayerAddr.

	struct MeetPlayer_Args {
		callerPlayerName string
		callerPlayerAddr string // Not needed by callee (All Bob's neighbors)

		// Note that Alice will not send her cluster map here
	}

- The reply of MeetPlayer is just an empty struct - we can add the `callerPlayer***` fields also, but unneeded.

- Alice will then *tell* each of her peers (except Bob) to invoke MeetPlayer RPC on each player of
  RemoteAddrOfPlayer. Alice does this *telling* by invoking MeetPlayerAndInformMe RPC.

	struct MeetEachPlayerOfCluster_Args {
		callerPlayerName string // Not really needed by Alice's neighbor
		callerPlayerAddr string // ^^^ ditto

		RemoteAddrOfPlayer map[string]addr // Bob's cluster map
	}


- Each of Alice's neighbor will invoke the MeetPlayer RPC on each player in bob's cluster map. On success,
  they will reply to Alice with

	struct MeetEachPlayerOfCluster_Reply {
		callerPlayerName string
		callerPlayerAddr string
	}

- Alice can make these calls to all of her neighbors synchronously - invoke and wait for reply in lockstep
  until all neighbors have been accounted for. Or she can make all the calls asynchronously and wait until
  they all finish.

- If we focus on the app at hand now - we can see that there will only be a couple of players. But this
  protocol will work for any N player and the async approach above can help cutting time. Although each of
  alice's neighbor should invoke the MeetPlayer RPC on the other cluster's peers in a random order/

------------------ FUCK ALL THIS ------------------------








Have a server idiot. Replicate THAT. Not the fucking clients. Idiot.



