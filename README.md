# hw-digest

GitHub Pages で配信するハードウェアニュースRSSです。

- `/akiba/` — 日本のハードウェア情報。RSS は `/akiba/index.xml`
- `/world/` — PC自作・コンポーネント・モバイル中心の海外情報。RSS は `/world/index.xml`

`sources.json` に収集元を定義しています。GitHub Actions が3時間ごとに実行され、公開日時が直近3時間以内の記事のみを収集します。`data/seen.json` はURLのSHA-256を30日保持するため、同じ記事を再配信しません。

海外側は、PCパーツ／自作PCを主題にする TechPowerUp・Tom's Hardware、ノートPC／スマートフォンを主題にする Notebookcheck・Android Authority・XDA、ハードウェア製品記事を持つ Ars Technica Gear & Gadgets を選定しています。

## 初回設定

リポジトリの **Settings → Pages → Build and deployment** で **GitHub Actions** を選び、`Update hardware feeds` を手動実行してください。公開URLは `https://jedipunkz.github.io/hw-digest/akiba/` と `https://jedipunkz.github.io/hw-digest/world/` になります。

## ローカル実行

```sh
go test ./...
go run ./cmd/hw-digest
```
