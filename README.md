# Fast

Test your internet speed from the command-line, powered by [fast.com](https://fast.com).

<img src="demo.gif" width="600" alt="fast running a speed test in the terminal" />

### Usage

```bash
fast
```

`fast` measures your download speed against the nearest Netflix Open Connect
servers and reports it in megabits per second, right inline in your terminal,
along with your ping to that server.

### Installation

Install with Go:

```sh
go install github.com/maaslalani/fast@main
```

Or download a binary from the [releases](https://github.com/maaslalani/fast/releases).

### This fork

The instructions above describe upstream. This fork uses [Cloudflare Speed Test](https://speed.cloudflare.com)
instead of Fast.com to measure download, upload and HTTP latency. Build and run it with Go:

```sh
git clone https://github.com/lw396/fast.git
cd fast
go build -o fast .
./fast
```

If traffic goes through a VPN or proxy, results reflect that path rather than your direct connection.

## License

[MIT](https://github.com/maaslalani/fast/blob/master/LICENSE)

## Feedback

I'd love to hear your feedback on improving `fast`.

Feel free to reach out via:
* [Email](mailto:maas@lalani.dev)
* [Twitter](https://twitter.com/maaslalani)
* [GitHub issues](https://github.com/maaslalani/fast/issues/new)

---

<sub><sub>z</sub></sub><sub>z</sub>z
