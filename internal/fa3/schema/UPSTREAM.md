# Vendored FA(3) schema

Source: Ministry of Finance KSeF documentation, `faktury/schemy/FA/` (schema FA(3), version 1-0E).

| File | SHA-256 (upstream) | Patched |
|---|---|---|
| `schemat_FA(3)_v1-0E.xsd` | `b646b6b525f51adf1bb2545f111fc8ca6e7aa6dd2f98948f1667d3695c06d958` | yes, one attribute |
| `bazowe/ElementarneTypyDanych_v10-0E.xsd` | `8daf4d3771de200b26b697294cc906a2add3de9acfbbd97f4b1bd4fc0e5ecb2f` | no |
| `bazowe/KodyKrajow_v10-0E.xsd` | `48be2a9f181d7ff80f185c62491ba12604c5cacbbe21af8e2aaaf2c585bbd214` | no |
| `bazowe/StrukturyDanych_v10-0E.xsd` | `cb08348374598e1e716e086c40d740390fb9e1bfa3aba1f4ec4cba0e1ef6d60f` | no |

## The one patch

Upstream `schemat_FA(3)_v1-0E.xsd` imports the base types over HTTP:

```
schemaLocation="http://crd.gov.pl/xml/schematy/dziedzinowe/mf/2022/01/05/eD/DefinicjeTypy/StrukturyDanych_v10-0E.xsd"
```

It is rewritten to the vendored copy so validation works offline (`xmllint --nonet`):

```
sed 's#schemaLocation="http://crd.gov.pl/xml/schematy/dziedzinowe/mf/2022/01/05/eD/DefinicjeTypy/StrukturyDanych_v10-0E.xsd"#schemaLocation="bazowe/StrukturyDanych_v10-0E.xsd"#'
```

The `bazowe/` files already import each other by relative path
(`ElementarneTypyDanych` includes `KodyKrajow`, `StrukturyDanych` includes
`ElementarneTypyDanych`), so they need no patch and are byte-identical to upstream.

`TestSchema_ImportPatched` reverses the patch and checks that the result has the upstream hash above,
so nothing but that attribute may differ. To update the schema, copy the new upstream files, re-apply
the sed, update the hashes here and in the test.
