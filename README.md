# piñata

Make your GitHub Actions usage more secure by pinning them to their SHA's.

<img width="1624" height="974" alt="CleanShot 2025-09-17 at 11 00 34@2x" src="https://github.com/user-attachments/assets/d7812b1f-e849-4da2-a54b-6475db657ddf" />


## Install

```sh
# Homebrew:
brew install caarlos0/tap/pinata

# Go:
go install github.com/caarlos0/pinata@latest
```

Or download from the [releases page](/releases).

## Usage

```console
$ pinata [dir] # Defaults to .github/workflows
$ pinata ./myrepo/.github/workflows
```
