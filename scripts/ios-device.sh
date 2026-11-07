#!/bin/sh
# ios-device.sh builds a package of native UI as an app of iOS, signs it
# with a development identity and a provisioning profile, installs it on a
# connected device and launches it.
#
#   IOS_PROFILE=<.mobileprovision> IOS_IDENTITY=<sha1 or name> \
#   scripts/ios-device.sh <package dir> <bundle id> <device> [args...]
set -e
pkg=$1; bundle=$2; dev=$3
shift 3
name=$(basename "$(cd "$pkg" && pwd)")
out=${IOS_OUT:-$(go env GOCACHE)/../mygo-ios-device}/$name.app
sdk=$(xcrun --sdk iphoneos --show-sdk-path)
rm -rf "$out"; mkdir -p "$out"
CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
CC="$(xcrun --sdk iphoneos -f clang) -isysroot $sdk -target arm64-apple-ios17.0" \
	go build -C "$pkg" -ldflags=-s -o "$out/$name" .
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
	<key>CFBundleSupportedPlatforms</key><array><string>iPhoneOS</string></array>
	<key>DTPlatformName</key><string>iphoneos</string>
	<key>MinimumOSVersion</key><string>17.0</string>
	<key>LSRequiresIPhoneOS</key><true/>
	<key>UIRequiredDeviceCapabilities</key><array><string>arm64</string></array>
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
	<key>UIFileSharingEnabled</key><true/>
	<key>LSSupportsOpeningDocumentsInPlace</key><true/>
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
cp "$IOS_PROFILE" "$out/embedded.mobileprovision"
ents=$(mktemp)
security cms -D -i "$IOS_PROFILE" > "$ents.profile"
team=$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.developer.team-identifier' "$ents.profile")
cat > "$ents" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>application-identifier</key><string>$team.$bundle</string>
	<key>com.apple.developer.team-identifier</key><string>$team</string>
	<key>get-task-allow</key><true/>
</dict>
</plist>
PLIST
codesign -f -s "$IOS_IDENTITY" --entitlements "$ents" --generate-entitlement-der "$out"
rm -f "$ents" "$ents.profile"
xcrun devicectl device install app --device "$dev" "$out"
xcrun devicectl device process launch --terminate-existing --device "$dev" "$bundle" "$@"
