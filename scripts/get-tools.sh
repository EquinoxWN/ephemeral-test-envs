#!/usr/bin/env bash
# Downloads pinned kind, helm and kubectl into .tmp/bin and checks each SHA-256 before use.
# usage: scripts/get-tools.sh [helm]   (only helm is needed for local lint and template checks)
set -euo pipefail

KIND_VERSION=v0.33.0
HELM_VERSION=v4.3.0
KUBECTL_VERSION=v1.37.1

cd "$(dirname "$0")/.."
BIN=.tmp/bin
mkdir -p "$BIN" .tmp/dl

case "$(uname -s)" in
  Linux) OS=linux EXE= ;;
  MINGW*|MSYS*|CYGWIN*) OS=windows EXE=.exe ;;
  *) echo "unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
[ "$(uname -m)" = x86_64 ] || { echo "only amd64 is pinned" >&2; exit 1; }

# sha256 values from the official release checksum files, pinned here.
declare -A SUM=(
  [kind-linux]=aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d
  [kind-windows]=4b22adaa135368c5a465d56bbd8e520cbea87272a06ca00b6078e7b81515c9fc
  [helm-linux]=86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb
  [helm-windows]=304ea163cce4d9ad14e189c01846c6a34de9cfdfe48536ae54b2e8ba7884e67c
  [kubectl-linux]=65691ff77eb6fa44c908b77a1082c9f092c3b9733b5cefabec0d1104890e21a8
)

# fetch downloads a URL over HTTPS and refuses it unless the SHA-256 matches.
fetch() {
  local url=$1 out=$2 want=$3
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$out" "$url"
  local got
  got=$(sha256sum "$out" | cut -d' ' -f1)
  [ "$got" = "$want" ] || { echo "checksum mismatch for $url: got $got, want $want" >&2; rm -f "$out"; exit 1; }
}

if [ ! -x "$BIN/helm$EXE" ]; then
  if [ "$OS" = linux ]; then
    fetch "https://get.helm.sh/helm-$HELM_VERSION-linux-amd64.tar.gz" .tmp/dl/helm.tgz "${SUM[helm-linux]}"
    tar -xzf .tmp/dl/helm.tgz -C .tmp/dl linux-amd64/helm && mv .tmp/dl/linux-amd64/helm "$BIN/helm"
  else
    fetch "https://get.helm.sh/helm-$HELM_VERSION-windows-amd64.zip" .tmp/dl/helm.zip "${SUM[helm-windows]}"
    unzip -o -q .tmp/dl/helm.zip windows-amd64/helm.exe -d .tmp/dl && mv .tmp/dl/windows-amd64/helm.exe "$BIN/helm.exe"
  fi
fi
[ "${1:-all}" = helm ] && { "$BIN/helm$EXE" version --short; exit 0; }

if [ ! -x "$BIN/kind$EXE" ]; then
  fetch "https://github.com/kubernetes-sigs/kind/releases/download/$KIND_VERSION/kind-$OS-amd64" "$BIN/kind$EXE" "${SUM[kind-$OS]}"
  chmod +x "$BIN/kind$EXE"
fi
if [ "$OS" = linux ] && [ ! -x "$BIN/kubectl" ]; then
  fetch "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl" "$BIN/kubectl" "${SUM[kubectl-linux]}"
  chmod +x "$BIN/kubectl"
fi
"$BIN/helm$EXE" version --short
"$BIN/kind$EXE" version
