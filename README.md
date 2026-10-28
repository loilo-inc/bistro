bistro
---

A Build utility to bake distroless image.

## Usage

Hooking script and trace `.deb` package installations, creating rootfs.

```bash
bistro save -o ./berofe-pkgs.json
apt install libssl-dev
bistro patch --before ./before-pkgs.json -o ./rootfs.tar
```

is (mostly) equivalent to:

```bash
apt install libssl-dev libyaml-dev -y
bistro collect -o ./rootfs.tar libssl3t63 libyaml-dev
```

```bash
bistro bundle -o ./rootfs.tar /usr/local/bin/ruby
```
