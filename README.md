# hw-digest

GitHub Pages で配信するハードウェアニュースダイジェストです。

- `/akiba/` — 日本のハードウェア情報
- `/world/` — PC自作・コンポーネント・モバイル中心の海外情報

`sources.json` に収集元を定義しています。GitHub Actions が3時間ごとに実行され、公開日時が直近3時間以内の記事のみを収集します。`data/seen.json` はURLのSHA-256を30日保持するため、同じ記事を再配信しません。

海外側は、PCパーツ／自作PCを主題にする TechPowerUp・Tom's Hardware、ノートPC／スマートフォンを主題にする Notebookcheck・Android Authority・XDA、ハードウェア製品記事を持つ Ars Technica Gear & Gadgets を選定しています。

## 初回設定

リポジトリの **Settings → Pages → Build and deployment** で **GitHub Actions** を選び、`Update hardware feeds` を手動実行してください。公開URLは `https://jedipunkz.github.io/hw-digest/akiba/` と `https://jedipunkz.github.io/hw-digest/world/` になります。

## アクセストークン

各ページの記事本文は AES-256-GCM で暗号化して埋め込まれ、正しいトークンを渡したときだけブラウザ側（Web Crypto API）で復号・表示されます。トークンは URL クエリで渡します。

```
https://jedipunkz.github.io/hw-digest/akiba/?token=<TOKEN>
https://jedipunkz.github.io/hw-digest/world/?token=<TOKEN>
```

トークンは GitHub Actions の **Settings → Secrets and variables → Actions** に `DIGEST_TOKEN` という名前で登録します。鍵は実行時に `PBKDF2-HMAC-SHA256`（20万回）で導出されます。`DIGEST_TOKEN` が未設定の場合、ページは暗号化されず平文で生成されます。

## ローカル実行

```sh
go test ./...
DIGEST_TOKEN=<TOKEN> go run ./cmd/hw-digest
```
