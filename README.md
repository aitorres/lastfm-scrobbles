# lastfm-scrobbles

A light server-side API that returns what a given user is scrobbling on [last.fm](https://www.last.fm/), written in Go.

Keep your last.fm API key secret by deploying this API as a sidecar to your front-end application.

## Usage

To run with Last.fm polling, build the Go application and set both Last.fm environment variables:

```bash
go build -o lastfm-scrobbles ./cmd/lastfm-scrobbles
LASTFM_USERNAME=your_lastfm_username LASTFM_API_KEY=your_lastfm_api_key ./lastfm-scrobbles
```

To run with webhook updates only:

```bash
WEBHOOK_API_KEY=your_webhook_key ./lastfm-scrobbles
```

Set both Last.fm variables and `WEBHOOK_API_KEY` to enable both sources. The most recent successful update from either source replaces the shared cache. At least one complete source configuration is required.

To deploy, you can build a Docker image using the provided [`Dockerfile`](./Dockerfile) or pull the pre-built image from ghcr.io:

```bash
docker pull ghcr.io/aitorres/lastfm-scrobbles:latest
```

## Environment Variables

- `LASTFM_USERNAME`: Last.fm username. Set with `LASTFM_API_KEY` to enable polling.
- `LASTFM_API_KEY`: Last.fm API key. Set with `LASTFM_USERNAME` to enable polling.
- `WEBHOOK_API_KEY`: API key for webhook updates. Enables `POST /webhook`.

Send the webhook key in the `X-API-Key` header. Send only fields needed for the current track:

```json
{"type":"nowPlaying","play":{"play":{"data":{"track":{"name":"Song"},"artists":[{"name":"Artist"}]}}}}
```

Send `{"type":"nowPlaying","play":null}` to clear the current track. The webhook returns `200` after it updates the cache. In webhook-only mode, `/is-scrobbling` assumes no track is playing until the first webhook update.

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

Please make sure to update tests as appropriate; a minimum coverage of 80% is expected (and enforced by Github Actions!).

## License

This project is licensed under the [GNU Affero General Public License v3.0](https://github.com/aitorres/lastfm-scrobbles/blob/main/LICENSE).
