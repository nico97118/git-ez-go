# Migrating from Python git-ez

The Python functional reference is pinned to commit
`69693f61ea5a1d668f103eb3dec0309efde08dbe`. Project-owned Go sources retain
GPL-3.0-only licensing and reference attribution; see NOTICE and LICENSE.

The executable and public product name remain `git-ez`, discovered as `git ez`.
Remove competing installations from PATH or select the intended executable
explicitly. Go distribution binaries do not require the Python SDK environment.
The existing native `ez.*` Git configuration remains the configuration surface.

`ez.agentMaxContextBytes` is removed. Existing values, including malformed ones,
are ignored. Remove obsolete values in the relevant scope with:

```sh
git config --unset ez.agentMaxContextBytes
```

There is no replacement byte-limit setting. Genkit manages context using declared
model capacity and output reservation. Diff, validation-output and aggregate audit
storage are not constrained by the former context-byte ceiling. Meaningful file,
execution and request/tool policies remain; see the configuration reference.

Git 2.38 is the supported minimum. Older native Git releases can lose a
requested `Signed-off-by` trailer when continuing a conflicted rebase; the
maintainer accepts this upstream limitation. Git-ez delegates continuation
without repairing messages or native metadata. This is independent of GPG/SSH
cryptographic commit signatures. Git 2.46.0 includes the upstream fix; a recent
Git is recommended. See [Git compatibility](git-compatibility.md).

Schema 4 is retained with LLM-focused evidence. Model-free operations do not create
persistent audit runs. Reading historical Python journals is best-effort; identical
writer layout and byte-identical report output are not requirements. Copy a journal
and use explicit-path offline consultation when needed. Do not automatically
convert or erase historical journals. Complete functional parity and real-model
semantic quality remain unqualified; consult the maintained acceptance gaps.
