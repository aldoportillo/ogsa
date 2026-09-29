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
