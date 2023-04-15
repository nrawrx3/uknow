package admin

/*
func (admin *Admin) tellExistingPlayersAboutNew(ctx context.Context, newPlayerName, newPlayerHost string, newPlayerPort int) {
	// Message is same for all players. Create it.
	var addPlayerMsg messages.AddNewPlayersMessage
	addPlayerMsg.Add(newPlayerName, newPlayerHost, newPlayerPort, "http")

	ctxForAllRequests, cancelFunc := context.WithTimeout(ctx, allPlayersSyncCommandTimeout)
	defer cancelFunc()
	g, ctx := errgroup.WithContext(ctxForAllRequests)

	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		playerListenAddr := playerListenAddr
		playerName := playerName

		g.Go(func() error {
			url := playerListenAddr.HTTPAddressString() + "/players"
			admin.logger.Printf("telling existing player %s at url %s about new player %s at url %s", playerName, playerListenAddr.HTTPAddressString(), newPlayerName, url)

			var b bytes.Buffer
			messages.EncodeJSONAndEncrypt(&addPlayerMsg, &b, admin.aesCipher)

			requestSender := utils.RequestSender{
				Client:     admin.httpClient,
				Method:     "POST",
				URL:        url,
				BodyReader: &b,
			}

			resp, err := requestSender.Send(ctx)

			if err != nil {
				return err
			}

			if resp.StatusCode != http.StatusOK {
				admin.logger.Printf("response from existing player %s on /player: %s", playerName, resp.Status)
				return fmt.Errorf("failed to call POST /players on player %s", playerName)
			}

			// Add an expecting-ack for this existing player. The existing player will send an ack asynchronously denoting that it has established connection with the new player
			admin.expectedAcksList.addPending(
				expectedAck{
					ackId:           makeAckIdConnectedPlayer(playerName, newPlayerName),
					ackerPlayerName: playerName,
				},
				5*time.Second,
				func() {},
				func() {
					admin.logger.Printf("ack timeout: existing player %s could not connect to new player %s in time", playerName, newPlayerName)
				},
			)

			admin.logger.Printf("Done telling existing player %s about new player %s at url %s, awaiting ack", playerName, newPlayerName, url)

			return nil
		})
	}

	err := g.Wait()

	if err != nil {
		admin.logger.Printf("Failed to add new player to one or more other players: %s", err)
	}

	admin.logger.Printf("listenAddrOfPlayer: %+v", admin.listenAddrOfPlayer)
}

// Req:		POST /player AddNewPlayerMessage
// Resp:	AddNewPlayerMessage
func (admin *Admin) handleAddNewPlayer(w http.ResponseWriter, r *http.Request) {
	admin.logger.Printf("addNewPlayer receeived from %s", r.RemoteAddr)

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.state != AddingPlayers {
		fmt.Fprintf(w, "Not accepting new players, currently in state: %s", admin.state)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// Parse message
	var msg messages.AddNewPlayersMessage

	err := messages.DecryptAndDecodeJSON(&msg, r.Body, admin.aesCipher)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(msg.ClientListenAddrs) != 1 || len(msg.PlayerNames) != 1 {
		admin.logger.Print("Bad request. Must have exactly 1 player and the listen address in AddNewPlayerMessage")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	newPlayerName := msg.PlayerNames[0]
	newPlayerAdvertisedAddr := msg.ClientListenAddrs[0]
	// Tell existing players about the new player
	admin.logger.Printf("newPlayerName = %s, newPlayerHost = %s, newPlayerPort = %d", newPlayerName, newPlayerAdvertisedAddr.IP, newPlayerAdvertisedAddr.Port)

	// Add the player to the local table. **But don't if it's already added
	// by hand-reader - in which case check that we have this player in the
	// table module.**
	if admin.table.IsShuffled {
		_, ok := admin.table.HandOfPlayer[newPlayerName]
		if !ok {
			admin.logger.Printf("player %s has not been loaded by hand-reader. see the JSON config.", newPlayerName)
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
	} else {
		err = admin.table.AddPlayer(newPlayerName)
		if errors.Is(err, uknow.ErrPlayerAlreadyExists) {
			w.WriteHeader(http.StatusOK)
			admin.logger.Printf("player %s already exists", newPlayerName)
			return
		}

		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			admin.logger.Printf("Cannot add new player: %s", err)
			return
		}
	}

	// Add the player's listen address
	admin.listenAddrOfPlayer[newPlayerName] = newPlayerAdvertisedAddr
	// Set it as shuffler, although it doesn't matter
	admin.shuffler = newPlayerName

	// Tell existing players about new player asynchronously
	go admin.tellExistingPlayersAboutNew(context.Background(), newPlayerName, newPlayerAdvertisedAddr.IP, newPlayerAdvertisedAddr.Port)

	// Tell the new player about existing players. This is by sending AddNewPlayersMessage as a response containing the existing players info.
	var respAddNewPlayersMessage messages.AddNewPlayersMessage
	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		admin.logger.Printf("Telling existing player '%s' about '%s'", playerName, newPlayerName)
		// addr, err := utils.ResolveTCPAddress(playerListenAddr.HTTPAddressString())
		// if err != nil {
		// 	admin.logger.Printf("Failed to resolve playerListenAddr. %s", err.Error())
		// 	w.WriteHeader(http.StatusInternalServerError)
		// 	messages.WriteErrorPayload(w, err)
		// 	continue
		// }
		respAddNewPlayersMessage.Add(playerName, playerListenAddr.IP, playerListenAddr.Port, "http")

		// Also add an expecting-ack that admin should receive from the new player for connecting to each of the existing players.
		admin.expectedAcksList.addPending(expectedAck{ackerPlayerName: newPlayerName, ackId: makeAckIdConnectedPlayer(newPlayerName, playerName)}, 10*time.Second, func() {}, func() {
			admin.logger.Printf("Ack timeout: new player %s could not connect to existing player %s in time", newPlayerName, playerName)
		})
	}

	messages.EncodeJSONAndEncrypt(&respAddNewPlayersMessage, w, admin.aesCipher)
}

*/
