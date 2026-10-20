// swift-tools-version: 5.9
// GregaleAuth: native client for a Gregale application Auth app (ADR-1190).
import PackageDescription

let package = Package(
    name: "GregaleAuth",
    platforms: [.iOS(.v15), .macOS(.v12)],
    products: [.library(name: "GregaleAuth", targets: ["GregaleAuth"])],
    targets: [
        .target(name: "GregaleAuth", swiftSettings: [.enableUpcomingFeature("StrictConcurrency")]),
        .testTarget(name: "GregaleAuthTests", dependencies: ["GregaleAuth"], swiftSettings: [.enableUpcomingFeature("StrictConcurrency")]),
    ]
)
