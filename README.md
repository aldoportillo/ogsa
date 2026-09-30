# Også

## Getting Started

## Running the CLI

### Option 1: Development

go run ./cmd/ogsa

### Option 2: Build Binary

go build -o ogsa ./cmd/ogsa
./ogsa

### Option 3: Install in Path

go install ./cmd/ogsa
ogsa



## CLI Commands

- /model -- select model from a list of providers. Manage providers and models.
  - This also checks to ensure that the model you selected has tool support
  - If the selected model does not support models you will be alerted

## Competitors

- [PI dev](https://pi.dev/) - Seems pretty cool. They also have an entire package ecosystem. Tried it out but seems to be confusing on model configuration setup for non cloud hosted models. [Repo](https://github.com/earendil-works/pi)
- [OpenCode](https://opencode.ai/) - Seems better. Don't like the javascript terminal. It was able to find which models I had installed locally. So setup seems simpler than PI dev. Both are in the npm ecosystem. Not a fan can introduce more bulk than not. [Repo](https://github.com/anomalyco/opencode)
- Også - Adding this here. Well similar mission but with a focus on simplicity and security. There have been so many npm supply chain attacks, so I am writing this in Go.
