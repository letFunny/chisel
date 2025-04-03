mkdir results 2>/dev/null

check() {
	OUT="$1-$2.out"
	RELEASE=$1 ARCH=$2 go run .github/scripts/check-cohesion-release.go 2>/dev/null > "results/$OUT"
}

ARCHS="amd64 arm64 armhf i386 ppc64el riscv64 s390x"
for arch in $ARCHS
do
	echo "processing ubuntu-24.04-$arch"
	check "ubuntu-24.04" "$arch"
	echo "processing ubuntu-24.10-$arch"
	check "ubuntu-24.10" "$arch"
done
