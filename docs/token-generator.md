# Token 生成工具

目录：`tools/token-generator`

示例：

```powershell
go run ./tools/token-generator -public-key .\config-templates\public_key.pem -payload .\config-templates\payload.generated.json -out .\config-templates\token.generated.txt
```

支持模式：

- `pkcs1v15`（默认）
- `oaep-sha256`

