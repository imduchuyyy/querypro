#!/bin/sh
set -eu
v=$1
url=https://github.com/imduchuyyy/querypro/releases/download/$v
tarball() {
	f=dist/querypro-$v-$1.tar.gz
	echo "      url \"$url/${f#dist/}\""
	echo "      sha256 \"$(shasum -a 256 "$f" | cut -d' ' -f1)\""
}
cat <<RUBY
class Querypro < Formula
  desc "Terminal UI for Postgres, MongoDB, Redis, RabbitMQ, Kafka and Loki"
  homepage "https://github.com/imduchuyyy/querypro"
  version "${v#v}"

  depends_on "node"

  on_macos do
    on_arm do
$(tarball darwin-arm64)
    end
    on_intel do
$(tarball darwin-amd64)
    end
  end

  on_linux do
    on_arm do
$(tarball linux-arm64)
    end
    on_intel do
$(tarball linux-amd64)
    end
  end

  def install
    libexec.install Dir["*"]
    bin.install_symlink libexec/"querypro"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/querypro --version")
  end
end
RUBY
