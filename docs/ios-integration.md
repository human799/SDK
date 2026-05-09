# iOS 客户端对接 SDK 文档

## 1. 前提

- 你已经有 `sdk.xcframework`
- iOS 工程可正常编译

---

## 2. 集成步骤

1. 将 `sdk.xcframework` 拖入 Xcode 工程
2. 勾选 `Copy items if needed`
3. 在 Target -> General -> `Frameworks, Libraries, and Embedded Content` 确认已加入

---

## 3. 启动流程

固定顺序：

1. `newSDKBootstrap()`
2. `setDataDir(...)`（推荐）
3. `init(secret)`
4. `prepare()`
5. `setLocalPort(...)`
6. `start()`
7. `localPort()` 获取端口给网络层

---

## 4. Swift 示例

```swift
import Foundation
import sdk

final class SDKManager {
    private var bootstrap: SDKSDKBootstrap?

    func start(secret: String) {
        let b = SDKSdk.newSDKBootstrap()

        if let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first {
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            b.setDataDir(dir.path)
        }

        do {
            try b.init(secret)
            try b.prepare()
            try b.setLocalPort(0) // 0 = 随机端口,测试环境  可以填 9527
            try b.start()

            let localPort = b.localPort()
            print("SDK started, localPort=\(localPort), status=\(b.status())")
            self.bootstrap = b
        } catch {
            print("SDK start failed: \(error)")
            print("SDK status: \(b.status())")
        }
    }

    func stop() {
        bootstrap?.stop()
    }
}
```

---

## 5. Objective-C 示例

```objc
#import <Foundation/Foundation.h>
#import <sdk/sdk.h>

@interface SDKManager : NSObject
@property (nonatomic, strong) SDKSDKBootstrap *bootstrap;
@end

@implementation SDKManager

- (void)startWithSecret:(NSString *)secret {
    SDKSDKBootstrap *b = [SDKSdk newSDKBootstrap];

    NSArray<NSURL *> *dirs = [[NSFileManager defaultManager] URLsForDirectory:NSApplicationSupportDirectory
                                                                     inDomains:NSUserDomainMask];
    if (dirs.count > 0) {
        NSURL *dir = dirs.firstObject;
        [[NSFileManager defaultManager] createDirectoryAtURL:dir
                                 withIntermediateDirectories:YES
                                                  attributes:nil
                                                       error:nil];
        NSError *dirErr = nil;
        [b setDataDir:dir.path error:&dirErr];
        if (dirErr) {
            NSLog(@"setDataDir error: %@", dirErr);
        }
    }

    NSError *err = nil;
    [b init:secret error:&err];
    if (err) { NSLog(@"init failed: %@", err); NSLog(@"status=%@", [b status]); return; }

    [b prepare:&err];
    if (err) { NSLog(@"prepare failed: %@", err); NSLog(@"status=%@", [b status]); return; }

    [b setLocalPort:0 error:&err];
    if (err) { NSLog(@"setLocalPort failed: %@", err); return; }

    [b start:&err];
    if (err) { NSLog(@"start failed: %@", err); NSLog(@"status=%@", [b status]); return; }

    NSLog(@"SDK started, localPort=%lld, status=%@", [b localPort], [b status]);
    self.bootstrap = b;
}

- (void)stop {
    [self.bootstrap stop];
}

@end
```

---

## 6. Secret 说明

`init(secret)` 支持：

- 明文 JSON / base64(JSON)（联调）
- RSA token（生产）

---

## 7. 常见排查

启动异常时先打印：

- `status_after_prepare`
- `status_after_start`
- `localPort`

关注 `status()` 字段：

- `prepared`
- `running`
- `local_port`
- `server_host` / `server_port`
- `state`
- `last_error`

