#!/bin/sh
set -eu
v=$1
url=https://github.com/imduchuyyy/querypro/releases/download/$v
tarball() {
	n=querypro-$v-$1
	echo "      url \"$url/$n.tar.gz\""
	echo "      sha256 \"$(shasum -a 256 "dist/$n.tar.gz" | cut -d' ' -f1)\""
	echo "      binary \"$n/querypro\""
}
cat <<RUBY
cask "querypro" do
  version "${v#v}"
  name "querypro"
  desc "Terminal UI for Postgres, MongoDB, Redis, RabbitMQ, Kafka and Loki"
  homepage "https://github.com/imduchuyyy/querypro"

  depends_on formula: "node"

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

  postflight do
    if OS.mac?
      system_command "/usr/bin/xattr",
                     args: ["-dr", "com.apple.quarantine", staged_path]
    end
  end
end
RUBY
