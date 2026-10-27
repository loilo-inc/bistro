bistro
---

A Build utility to bake distroless image.


## Usage

Hooking script and trace `.deb` package installations, creating rootfs.

```bash
bistro dpkg record -o /tmp/rootfs -- apt install libssl-dev libyaml-dev -y
```

is (mostly) equivalent to:

```bash
apt install libssl-dev libyaml-dev -y
bistro dpkg collect -o /tmp/rootfs libssl3t63 libyaml-dev
```

```bash
bistro ldd collect -o /tmp/rootfs /usr/local/bin/ruby
```
