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
                                EntryRow(entry: item.entry, detail: detail(item), showPlace: true)
                            }
                        }
                    }
                }
            }
        }
        .navigationTitle("Agenda")
        .refreshable { await model.refresh() }
    }

    private func detail(_ item: AgendaItem) -> String? {
        guard !item.date.isEmpty else { return nil }
        var s = "\(item.label == "Deadline" ? "Due" : "Scheduled") \(OrgDate.display(item.date))"
        if !item.repeater.isEmpty { s += " \(item.repeater)" }
        if item.missed > 0 { s += " (\(item.missed) missed)" }
        return s
    }
}
