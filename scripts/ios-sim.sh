#!/bin/sh
# ios-sim.sh builds a package of native UI as an app of the iOS simulator,
# installs it on a booted simulator, and launches it with its output here.
#
#   scripts/ios-sim.sh <package dir> <bundle id> [simulator] [args...]
set -e
pkg=$1; bundle=$2; dev=${3:-booted}
[ $# -ge 3 ] && shift 3 || shift $#
name=$(basename "$(cd "$pkg" && pwd)")
out=${IOS_OUT:-$(go env GOCACHE)/../mygo-ios}/$name.app
sdk=$(xcrun --sdk iphonesimulator --show-sdk-path)
mkdir -p "$out"
CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
CC="$(xcrun --sdk iphonesimulator -f clang) -isysroot $sdk -target arm64-apple-ios17.0-simulator" \
	go build -C "$pkg" -o "$out/$name" .
cat > "$out/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>$bundle</string>
	<key>CFBundleExecutable</key><string>$name</string>
	<key>CFBundleName</key><string>$name</string>
	<key>CFBundleDisplayName</key><string>${IOS_NAME:-$name}</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleVersion</key><string>1</string>
	<key>CFBundleShortVersionString</key><string>1.0</string>
	<key>CFBundleSupportedPlatforms</key><array><string>iPhoneSimulator</string></array>
	<key>MinimumOSVersion</key><string>17.0</string>
	<key>LSRequiresIPhoneOS</key><true/>
	<key>UIDeviceFamily</key><array><integer>1</integer><integer>2</integer></array>
	<key>UILaunchScreen</key><dict/>
	<key>UIApplicationSceneManifest</key><dict>
		<key>UIApplicationSupportsMultipleScenes</key><false/>
		<key>UISceneConfigurations</key><dict>
			<key>UIWindowSceneSessionRoleApplication</key><array><dict>
				<key>UISceneConfigurationName</key><string>Default</string>
				<key>UISceneDelegateClassName</key><string>MyGoSceneDelegate</string>
			</dict></array>
		</dict>
	</dict>
	<key>UISupportedInterfaceOrientations</key><array>
		<string>UIInterfaceOrientationPortrait</string>
		<string>UIInterfaceOrientationLandscapeLeft</string>
		<string>UIInterfaceOrientationLandscapeRight</string>
	</array>
	<key>UISupportedInterfaceOrientations~ipad</key><array>
		<string>UIInterfaceOrientationPortrait</string>
		<string>UIInterfaceOrientationPortraitUpsideDown</string>
		<string>UIInterfaceOrientationLandscapeLeft</string>
		<string>UIInterfaceOrientationLandscapeRight</string>
	</array>
</dict>
</plist>
PLIST
for f in ${IOS_RESOURCES:-}; do cp "$f" "$out/"; done
codesign -f -s - "$out" >/dev/null 2>&1 || true
xcrun simctl install "$dev" "$out"
xcrun simctl launch --console-pty --terminate-running-process "$dev" "$bundle" "$@"
