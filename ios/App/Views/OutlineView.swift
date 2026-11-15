import SwiftUI

/// The read-only outline: each file's headlines as an expandable tree, a
/// separate Reference section, and search. A row's chevron expands or
/// collapses it in place; tapping the row itself opens its detail.
/// Picking a search result opens the outline at that entry.
struct OutlineView: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var results: [Entry] = []
    @State private var expanded: Set<String> = []
    @State private var target: String?

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
                            EntryRow(entry: entry, showPlace: true)
                        }
                        .buttonStyle(.plain)
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
        .navigationTitle("Outline")
        .searchable(text: $query, prompt: "Search titles, tags, notes")
        .task(id: query) {
            results = query.isEmpty ? [] : await model.search(query)
        }
        .toolbar {
            ToolbarItem(placement: .secondaryAction) {
                Toggle("Show closed items", isOn: $model.showClosed)
            }
            ToolbarItem(placement: .secondaryAction) {
                Button("Collapse all") { expanded = [] }
            }
        }
        .refreshable { await model.refresh() }
    }

    /// The rows currently visible under `entries`: every entry whose
    /// ancestors are all expanded, in outline order.
    private func nodes(_ entries: [Entry]) -> some View {
        ForEach(TreeRow.visible(entries, expanded: expanded)) { row in
            TreeRowView(row: row, isExpanded: expanded.contains(row.id)) {
                withAnimation(.snappy(duration: 0.2)) {
                    if expanded.contains(row.id) { expanded.remove(row.id) } else { expanded.insert(row.id) }
                }
            }
        }
    }

    /// Leaves search, expands the entry's ancestors, and scrolls to it.
    private func reveal(_ entry: Entry) {
        expanded.formUnion(entry.ancestorIDs)
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

private struct TreeRowView: View {
    let row: TreeRow
    let isExpanded: Bool
    let toggle: () -> Void

    private var children: [Entry] { row.entry.children ?? [] }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 2) {
            if children.isEmpty {
                Color.clear.frame(width: 28, height: 1)
            } else {
                Button(action: toggle) {
                    Image(systemName: "chevron.right")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(isExpanded ? 90 : 0))
                        .frame(width: 28, height: 28)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(isExpanded ? "Collapse" : "Expand")
            }
            NavigationLink(value: row.entry) {
                HStack(alignment: .firstTextBaseline) {
                    EntryRow(entry: row.entry)
                    if !children.isEmpty && !isExpanded {
                        Text("(\(Self.descendants(children)))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .padding(.leading, CGFloat(row.depth) * 16)
        .listRowInsets(EdgeInsets(top: 4, leading: 8, bottom: 4, trailing: 16))
    }

    /// How many entries a collapsed row hides, like the desktop's "(N)".
    static func descendants(_ entries: [Entry]) -> Int {
        entries.reduce(0) { $0 + 1 + descendants($1.children ?? []) }
    }
}
