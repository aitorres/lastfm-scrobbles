# lastfm-scrobbles

A light server-side API that returns what a given user is scrobbling on [last.fm](https://www.last.fm/), written in Go.

Keep your last.fm API key secret by deploying this API as a sidecar to your front-end application.

## Usage

To run locally, build the Go application and set the required environment variables, then run:

```bash
go build -o lastfm-scrobbles main.go
LASTFM_USERNAME=your_lastfm_username LASTFM_API_KEY=your_lastfm_api_key ./lastfm-scrobbles
```

To deploy, you can build a Docker image using the provided [`Dockerfile`](./Dockerfile) or pull the pre-built image from ghcr.io:

```bash
docker pull ghcr.io/aitorres/lastfm-scrobbles:latest
```

## Environment Variables

- `LASTFM_USERNAME`: last.fm username (required)
- `LASTFM_API_KEY`: last.fm API key (required)

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

Please make sure to update tests as appropriate; a minimum coverage of 80% is expected (and enforced by Github Actions!).

## License

This project is licensed under the [GNU Affero General Public License v3.0](https://github.com/aitorres/lastfm-scrobbles/blob/main/LICENSE).
