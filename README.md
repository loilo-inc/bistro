# bistro

A Debian package bundler for distroless Docker/OCI images.

## Usage

### `bistro scan`

`scan` lists the ELF dependencies of an ELF executable.

For example, running this in `debian:latest`:

```bash
bistro scan /usr/bin/python3
```

Produces:

```txt
/usr/bin/python3
  /lib/aarch64-linux-gnu/libm.so.6 => libc6:arm64
  /lib/aarch64-linux-gnu/libz.so.1 => zlib1g:arm64
  /lib/aarch64-linux-gnu/libexpat.so.1 => libexpat1:arm64
  /lib/aarch64-linux-gnu/libc.so.6 => libc6:arm64
```

The scan result shows that:

- `/usr/bin/python3` depends on 4 ELF objects (`.so`) and 3 Debian packages.
- Files owned by these 3 packages (`libc6`, `zlib1g`, `libexpat1`) must be copied into the distroless image.

### `bistro bundle`

`bundle` bundles one or more ELF executables and their dependencies into a single rootfs tarball.

```bash
bistro bundle -o ./python3.tar /usr/bin/python3
```

### `bistro unpack`

`unpack` extracts one or more uncompressed `.tar` archives into a destination directory, creating the directory if needed.

Use `-d` (or `--dest`) to specify the destination. The default is the current directory.

```bash
bistro unpack ./python3.tar -d ./python3
```

## Limitations

`bistro bundle` cannot automatically collect every dependency. Many files are implicitly required by the application.

For example, shared libraries loaded via `dlopen(3)` cannot be collected automatically. You must identify them manually and include them using the `--package`, `--copy`, or `--scan` options of the `bundle` command.

Here is an example script for bundling the `chromium` browser:

```bash
bistro bundle /usr/lib/chromium/chromium \
  --scan /usr/lib/chromium \
  --package chromium \
  --package chromium-common \
  --copy /var/some/file \
  -o /chromium.rootfs.tar
```

- `--scan` adds a scan target to collect packages required by libraries linked to the executable.
- `--package` adds a Debian package to the tarball.
- `--copy` copies a file or directory into the tarball.

## LICENSE

MIT License

Copyright (c) 2026 LoiLo inc.
