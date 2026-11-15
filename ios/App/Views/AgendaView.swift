import SwiftUI

/// Overdue / Due Today / Upcoming / Next Actions, as desktop :agenda
/// (minus Meetings).
struct AgendaView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        List {
            if let agenda = model.agenda {
                if agenda.sections.isEmpty {
                    ContentUnavailableView("Nothing on the agenda", systemImage: "checkmark.circle")
                }
                ForEach(agenda.sections) { section in
                    Section(section.name) {
                        ForEach(section.items ?? []) { item in
                            NavigationLink(value: item.entry) {
                                CompactEntryRow(entry: item.entry, subtitle: subtitle(item))
                            }
                            .compactEntryRowInsets()
                        }
                    }
                }
            }
        }
        .compactEntryList()
        .navigationTitle("Agenda")
        .refreshable { await model.refresh() }
    }

    /// The date (in the timestamp color), then where the entry lives.
    private func subtitle(_ item: AgendaItem) -> Text {
        guard let date = detail(item) else { return Text(item.entry.place) }
        return Text(date).foregroundColor(model.dateColor) + Text(" · " + item.entry.place)
    }

    private func detail(_ item: AgendaItem) -> String? {
        guard !item.date.isEmpty else { return nil }
        var s = "\(item.label == "Deadline" ? "Due" : "Scheduled") \(OrgDate.display(item.date))"
        if !item.repeater.isEmpty { s += " \(item.repeater)" }
        if item.missed > 0 { s += " (\(item.missed) missed)" }
        return s
    }
}
