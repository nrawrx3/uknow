A game of UNO in the terminal.

## Running the server

Edit `test_configs/admin_config.json`.

Go to `cmd/admin` dir and run

`go run admin_app.go -conf ../../test_configs/admin_config.json`

## Running player clients

Edit `test_configs/<playername>_client_config.json`.

Go to `cmd/client` and run

`go run client_app.go -conf ../../test_configs/<playername>_client_config.json`
