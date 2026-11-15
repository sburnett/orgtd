import SwiftUI

/// The outline: each file's headlines as an expandable tree, a separate
/// Reference section, and search. A row's chevron expands or collapses
/// its children; tapping the row opens its details (and edit form) in
/// place. Picking a search result opens the outline at that entry.
struct OutlineView: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var results: [Entry] = []
    @State private var expanded: Set<String> = []
    @State private var target: String?
    @State private var open: String?

    var body: some View {
        @Bindable var model = model
        ScrollViewReader { proxy in
            List {
                if !query.isEmpty {
                    if results.isEmpty {
                        ContentUnavailableView.search(text: query)
                    }
                    ForEach(results) { entry in
                        Button {
                            reveal(entry)
                        } label: {
                            CompactEntryRow(entry: entry, subtitle: Text(entry.place))
                        }
                        .buttonStyle(.plain)
                        .compactEntryRowInsets()
                    }
                } else if let outline = model.outline {
                    ForEach(outline.files) { file in
                        Section(file.file) { nodes(file.entries) }
                    }
                    if !outline.reference.isEmpty {
                        ForEach(outline.reference) { file in
                            Section("Reference · \(file.file.replacingOccurrences(of: "reference/", with: ""))") {
                                nodes(file.entries)
                            }
                        }
                    }
                }
            }
            .onChange(of: target) { _, id in
                guard let id else { return }
                withAnimation { proxy.scrollTo(id, anchor: .center) }
                target = nil
            }
        }
        .compactEntryList()
        .navigationTitle("Outline")
        .searchable(text: $query, prompt: "Search titles, tags, notes")
        .task(id: query) {
            results = query.isEmpty ? [] : await model.search(query)
        }
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button {
                    model.showClosed.toggle()
                } label: {
                    Image(systemName: model.showClosed ? "eye" : "eye.slash")
                }
                .accessibilityLabel(model.showClosed ? "Hide closed items" : "Show closed items")

                // Collapses everything if anything is open; otherwise
                // opens everything. The icon shows what a tap will do.
                let expandable = allExpandable
                let anyOpen = !expanded.isDisjoint(with: expandable)
                Button {
                    withAnimation(.snappy(duration: 0.2)) {
                        expanded = anyOpen ? [] : expandable
                    }
                } label: {
                    Image(systemName: anyOpen ? "rectangle.compress.vertical" : "rectangle.expand.vertical")
                }
                .accessibilityLabel(anyOpen ? "Collapse all" : "Expand all")
                .disabled(!query.isEmpty || expandable.isEmpty)
            }
            // Sync in a capsule of its own, as on every other tab, so it
            // sits in exactly the same spot: grouped with the buttons above
            // it would share their capsule and be inset differently.
            if #available(iOS 26.0, *) {
                ToolbarSpacer(.fixed, placement: .topBarTrailing)
            }
            ToolbarItem(placement: .topBarTrailing) { SyncButton() }
        }
        .refreshable { await model.refresh() }
    }

    /// Every entry in the outline that has children, i.e. everything
    /// "expand all" opens.
    private var allExpandable: Set<String> {
        guard let outline = model.outline else { return [] }
        var ids = Set<String>()
        func walk(_ entries: [Entry]) {
            for e in entries {
                guard let children = e.children, !children.isEmpty else { continue }
                ids.insert(e.id)
                walk(children)
            }
        }
        for file in outline.files + outline.reference { walk(file.entries) }
        return ids
    }

    /// The rows currently visible under `entries`: every entry whose
    /// ancestors are all expanded, in outline order.
    private func nodes(_ entries: [Entry]) -> some View {
        ForEach(TreeRow.visible(entries, expanded: expanded)) { row in
            TreeRowView(row: row, isExpanded: expanded.contains(row.id), open: $open) {
                withAnimation(.snappy(duration: 0.2)) {
                    if expanded.contains(row.id) { expanded.remove(row.id) } else { expanded.insert(row.id) }
                }
            }
        }
    }

    /// Leaves search, expands the entry's ancestors, and scrolls to it.
    private func reveal(_ entry: Entry) {
        expanded.formUnion(entry.ancestorIDs)
        open = entry.id
        query = ""
        Task { @MainActor in
            // Let the outline rows exist before scrolling to one.
            try? await Task.sleep(for: .milliseconds(150))
            target = entry.id
        }
    }
}

/// One visible line of the tree.
struct TreeRow: Identifiable {
    let entry: Entry
    let depth: Int
    var id: String { entry.id }

    static func visible(_ entries: [Entry], expanded: Set<String>, depth: Int = 0) -> [TreeRow] {
        var rows: [TreeRow] = []
        for e in entries {
            rows.append(TreeRow(entry: e, depth: depth))
            if let children = e.children, !children.isEmpty, expanded.contains(e.id) {
                rows += visible(children, expanded: expanded, depth: depth + 1)
            }
        }
        return rows
    }
}

/// One line of the tree: indentation, a chevron for an entry with
/// children, then the shared EntryListRow (whose details open in place).
private struct TreeRowView: View {
    @Environment(AppModel.self) private var model
    let row: TreeRow
    let isExpanded: Bool
    @Binding var open: String?
    let toggle: () -> Void

    private var entry: Entry { row.entry }
    private var children: [Entry] { entry.children ?? [] }

    var body: some View {
        HStack(alignment: .top, spacing: 2) {
            if children.isEmpty {
                Color.clear.frame(width: 28, height: 1)
            } else {
                Button(action: toggle) {
                    Image(systemName: "chevron.right")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(isExpanded ? 90 : 0))
                        .animation(.snappy(duration: 0.2), value: isExpanded) // rotates for children, not for details
                        .frame(width: 28, height: entryLineHeight)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(isExpanded ? "Collapse" : "Expand")
                .transaction { $0.animation = nil } // still while the row's details open
            }
            EntryListRow(entry: entry, key: entry.id, open: $open, hiddenCount: children.isEmpty || isExpanded ? nil : Self.descendants(children))
        }
        .padding(.leading, CGFloat(row.depth) * 14)
        .listRowInsets(EdgeInsets(top: 0, leading: 6, bottom: 0, trailing: 12))
    }

    /// How many entries a collapsed row hides, like the desktop's "(N)".
    static func descendants(_ entries: [Entry]) -> Int {
        entries.reduce(0) { $0 + 1 + descendants($1.children ?? []) }
    }
}
