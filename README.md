# bistro

A Debian package bundler for distroless Docker/OCI images.

## Concept

`bistro` is a tool for building flexible distroless container images.

It does:

- Analyze the application's ELF files.
- Collect their required shared libraries and Debian packages.
- Bundle them into a single tarball.

Here is an example Dockerfile for a distroless Ruby image:

```Dockerfile
FROM ghcr.io/loilo-inc/bistro AS bistro
FROM ruby:4.0 AS ruby
RUN --mount=type=bind,from=bistro,source=/bin/bistro,target=/bin/bistro \
  bistro bundle /usr/local -o /ruby.tar \
  && mkdir /ruby && tar -xf /ruby.tar -C /ruby
FROM gcr.io/distroless/base:nonroot
COPY --from=ruby /ruby /
ENTRYPOINT ["/usr/local/bin/ruby"]
CMD ["-v"]
```

## Efficacy

In the example above, `bistro bundle` reduced the image size by 86% and CVEs by 99% while preserving SBOM (Software Bill of Materials) information.

```console
$ docker images | grep ruby
bistro-ruby:4.0  701a33fa4c27   205MB  60.1MB
ruby:4.0         d8279d65457f  1.65GB   437MB  U
```

Vulnerabilities were measured using Docker Scout on November 7, 2026.

| Vulnerabilities | `ruby:4.0` | `bistro-ruby:4.0` |
| --- | ---: | ---: |
| Total | 279 in 47 packages | 2 in 1 package |
| Critical | 2 | 0 |
| High | 28 | 1 |
| Medium | 25 | 0 |
| Low | 208 | 1 |
| Unspecified | 16 | — |

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

`bundle` bundles files, directories, and their ELF dependencies into a single rootfs tarball.

```bash
bistro bundle -o ./python3.rootfs.tar /usr/bin/python3
```

#### Limitations

`bistro bundle` cannot automatically collect every dependency. Many files are implicitly required by the application.

For example, shared libraries loaded via `dlopen(3)` cannot be collected automatically. You must identify them manually and include them as positional arguments or using `--package`.

Here is an example script for bundling the `chromium` browser:

```bash
bistro bundle -o /chromium.rootfs.tar \
  --package chromium \
  --package chromium-common \
  /usr/lib/chromium
```

- `--package` adds a Debian package to the tarball.

## LICENSE

MIT License

Copyright (c) 2026 LoiLo inc.
