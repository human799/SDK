# SDK 发布前自检

## 1) 运行测试

```powershell
go test ./...
```

## 2) 构建带本地私钥的 AAR

```powershell
.\scripts\build-aar-with-local-key.ps1
```

Git Bash:

```bash
./scripts/build-aar-with-local-key.sh
```

## 3) SDK 自检

```go
report := sdk.PreflightSelfCheck("")
```

