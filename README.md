# piñata

Make your GitHub Actions usage more secure by pinning them to their SHA's.

<img width="1930" height="994" alt="CleanShot 2025-09-24 at 01 45 46@2x" src="https://github.com/user-attachments/assets/bce6ec86-1274-4401-8fc7-8c5d23eaf462" />


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
