# hessed-os/mcp-go-sdk

A fork of [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) used by
[Sillok](https://github.com/hessed-os/sillok). It exists because Sillok needs two server features
that upstream v1.8.0 does not have yet, and waiting for them would stall Sillok's release:

- the MCP events extension (`events/list`, `events/subscribe`, `events/unsubscribe` and the
  `events` server capability), which ChatGPT uses to wake an MCP server's subscribers;
- a token check given to the Streamable HTTP handler as an option, so the handler itself answers
  401 with the OAuth discovery header and passes the token info to tool calls.

Rules for this fork:

- The `sillok` branch is the latest upstream release tag (now `v1.8.0`) plus the patches listed
  below. The module path is `github.com/hessed-os/mcp-go-sdk` so that `go install <module>@<version>`
  of programs that use it keeps working; a `replace` directive would break that.
- One commit per patch. Every patch is also offered upstream as a pull request. When upstream ships
  an equivalent feature, the patch is dropped, and when nothing is left the fork is retired and
  Sillok goes back to the upstream module.
- Tags are `v<upstream version>-sillok.<n>`.
- The license is upstream's, unchanged.

## Patches and their way out

Checked 2026-11-03 against upstream `v1.8.0` (2026-09-14), the latest release.

| Patch | Commit on `sillok` | Upstream status | Drop it when |
|---|---|---|---|
| Module path rename | `3e74281` | Not an upstream change | The fork retires |
| Token check as a Streamable HTTP handler option | `7c3af17` | Not offered upstream yet. Upstream already has `auth.RequireBearerToken` middleware, and its handler passes the token info from the request context to tool calls (`mcp/streamable.go` in `v1.8.0`) | Sillok wraps `/mcp` in the upstream middleware instead of using the option |
| MCP events extension | `8bf7e2c` | Not offered upstream yet. Related upstream issue [#1325](https://github.com/modelcontextprotocol/go-sdk/issues/1325) is open. The extension itself is still experimental ([experimental-ext-triggers-events](https://github.com/modelcontextprotocol/experimental-ext-triggers-events)) | Upstream ships an events API ChatGPT can use |

Update this table at every catch-up: commit hashes change after a rebase, and an upstream status
change is the signal to drop a patch.

## Catching up with upstream

Sillok needs the events patch for ChatGPT webhooks, so the fork stays until upstream has an
equivalent. To keep the cost of staying small, follow every upstream release instead of letting
versions pile up.

When:

- Within two weeks of each upstream release. Recent releases came 14, 67 and 48 days apart.
- Check once a month even without news: `gh release list -R modelcontextprotocol/go-sdk --limit 3`.

Steps, for an upstream release `vX.Y.Z`:

1. Fetch upstream: `git remote add upstream https://github.com/modelcontextprotocol/go-sdk.git`
   (once), then `git fetch upstream --tags`.
2. Read the upstream release notes and diff for each patch's area (`mcp/streamable.go`,
   `mcp/server.go`, `auth/`). If upstream now has an equivalent, drop that patch in the next step and
   mark it in the table above.
3. Replay the patches on the new tag: `git rebase --onto vX.Y.Z <previous upstream tag> sillok`.
   On conflicts in the module rename commit, redo the rename instead of resolving by hand:
   `go mod edit -module github.com/hessed-os/mcp-go-sdk`, rewrite the import path in all `.go`
   files, then `gofmt -w .`.
4. Run the whole suite: `go test ./...`. Upstream tests and the patches' own tests must pass.
5. Push the rebased branch (`git push --force-with-lease origin sillok`) and tag it
   `vX.Y.Z-sillok.1` (`git tag` + `git push origin <tag>`). Old tags stay, so older Sillok builds
   keep resolving.
6. In Sillok, `go get github.com/hessed-os/mcp-go-sdk@vX.Y.Z-sillok.1`, run Sillok's MCP tests
   (token check and events), and open a pull request there.
7. Update the table above: new commit hashes, upstream status, dropped patches.

Offering the patches upstream is still owed (see the table). When one is opened, link its pull
request in the table.
