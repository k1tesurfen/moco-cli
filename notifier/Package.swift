// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MocoNotifier",
    platforms: [.macOS("26.0")],
    targets: [
        .executableTarget(
            name: "MocoNotifier",
            path: "Sources/MocoNotifier",
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
    ]
)
