import Foundation

/// The App Group container shared by the app and the share extension
/// (see ios/DESIGN.md §3.2). Its identifier is derived from the app's
/// bundle identifier, so setting BUNDLE_ID_PREFIX in Signing.local.xcconfig
/// is all it takes to make it unique.
enum AppGroup {
    /// The containing app's bundle identifier, from either target.
    static var appBundleID: String {
        let id = Bundle.main.bundleIdentifier ?? "orgtd"
        return id.hasSuffix(".share") ? String(id.dropLast(".share".count)) : id
    }

    static var identifier: String { "group." + appBundleID }

    static var isShared: Bool { sharedURL != nil }

    private static var sharedURL: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: identifier)
    }

    /// The shared container, or — when the App Group entitlement is
    /// missing (an unsigned simulator build, or a signing team that
    /// doesn't allow groups; see risk R1) — the app's own Application
    /// Support directory. In that fallback the share extension can't
    /// reach the app's files; `isShared` says which one this is.
    private static var rootURL: URL {
        if let url = sharedURL { return url }
        return FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("orgtd", isDirectory: true)
    }

    /// Where everything orgtd stores lives: the clone, the capture queue,
    /// sync state, incoming share-extension captures, the tags cache. See
    /// `prepare` for how it's protected.
    static var containerURL: URL {
        let url = rootURL.appendingPathComponent("data", isDirectory: true)
        prepare(url)
        return url
    }

    /// Makes `dir` (idempotently) with the strictest file protection — its
    /// contents are encrypted and unreadable whenever the phone is locked;
    /// files created in it later inherit that — and excludes it, contents
    /// included, from iCloud and computer backups: the org files are
    /// sensitive, and the clone can always be fetched again. (Captures not
    /// yet synced exist only here, so they'd be lost with the phone.)
    static func prepare(_ dir: URL) {
        let fm = FileManager.default
        try? fm.createDirectory(at: dir, withIntermediateDirectories: true,
                                attributes: [.protectionKey: FileProtectionType.complete])
        try? fm.setAttributes([.protectionKey: FileProtectionType.complete], ofItemAtPath: dir.path)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        var url = dir
        try? url.setResourceValues(values)
    }

    /// The clone's directory, pre-created so it gets the protection
    /// class before git writes anything into it.
    static var repoURL: URL {
        let url = containerURL.appendingPathComponent("repo", isDirectory: true)
        prepare(url)
        return url
    }

    /// Where the share extension drops captures, one JSON file each; the
    /// app imports them into the queue (capturequeue.Import) on refresh.
    static var incomingURL: URL { containerURL.appendingPathComponent("incoming", isDirectory: true) }

    /// Every tag in the org files, written by the app after each refresh
    /// so the share extension (which doesn't link the Go core) can
    /// autocomplete them too.
    static var tagsCacheURL: URL { containerURL.appendingPathComponent("tags-cache.json") }
}
