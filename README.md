bistro
---

A Build utility to bake distroless image.

## Usage

Hooking script and trace `.deb` package installations, creating rootfs.

```bash
bistro dpkg save -o /tmp/berofe-pkgs.json
apt install libssl-dev
bistro dpkg patch --before /tmp/before-pkgs.json -o /tmp/rootfs.tar
```

is (mostly) equivalent to:

```bash
apt install libssl-dev libyaml-dev -y
bistro dpkg collect -o /tmp/rootfs.tar libssl3t63 libyaml-dev
```

```bash
bistro ldd collect -o /tmp/rootfs.tar /usr/local/bin/ruby
```
