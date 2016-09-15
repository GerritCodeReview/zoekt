#!/bin/sh

# this script packages up all the binaries, and a script (deploy.sh)
# to twiddle with the server and the binaries

set -eux
mkdir -p bin


VERSION=$(date --iso-8601=minutes | tr -d ':' | sed 's|\+.*$||')
if [[ -d .git ]]; then
  VERSION=$(git show --pretty=format:%h -q)-${VERSION}
fi

for d in cmd/*
do
    go build -ldflags "-X main.Version=$VERSION"  -o bin/$d github.com/google/zoekt/$d
done

cat <<EOF > bin/deploy.sh
#!/bin/bash

echo "Set the following in the environment."
echo ""
echo '  export PATH="'$PWD'/bin:$PATH'
echo ""

set -eux

# Allow sandbox to create NS's
sudo sh -c 'echo 1 > /proc/sys/kernel/unprivileged_userns_clone'

# we mmap the entire index, but typically only want the file contents.
sudo sh -c 'echo 1 >/proc/sys/vm/overcommit_memory'

# allow bind to 80 and 443
sudo setcap 'cap_net_bind_service=+ep' bin/zoekt-webserver

EOF

chmod +x bin/deploy.sh

zip zoekt-deploy.${VERSION}.zip bin/*
