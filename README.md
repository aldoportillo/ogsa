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

- /model -- Select a model from a list of providers. Manage providers and models.
  - This also checks to ensure that the selected model has tool support.
  - If the selected model does not support tools, you will be alerted.

## Competitors

- [PI dev](https://pi.dev/) - Seems pretty cool. They also have an entire package ecosystem. I tried it out, but the model configuration setup for non-cloud-hosted models seemed confusing. [Repo](https://github.com/earendil-works/pi)
- [OpenCode](https://opencode.ai/) - Seems better. I don't like the JavaScript terminal. It was able to find which models I had installed locally, so the setup seems simpler than PI dev. Both are in the npm ecosystem. I am not a fan, as it can introduce more bulk than necessary. [Repo](https://github.com/anomalyco/opencode)
- Også - Adding this here. It has a similar mission but with a focus on simplicity and security. There have been so many npm supply chain attacks, so I am writing this in Go.
