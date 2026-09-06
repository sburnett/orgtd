# orgtd

orgtd is a portmanteau of "org-mode" and "getting things done". It's a TUI application written in Go for implementing a "getting things done" workflow using org files as the backend. It doesn't attempt to be a full-fledged org file editor; it just implements a subset useful for the GTD method.

## Why not just use org-mode?

emacs and org-mode are incredibly flexible, but that flexibility can be overwhelming. orgtd is meant to be a simpler alternative supporting a single workflow so you can focus less on customizing emacs and more on getting things done. In particular, since orgtd is tailored towards one use case we can use much more natural keybindings.

## Workflows

There are several core workflows we need to support:

### Inbox capture

The most common operation is capturing a new item in the inbox. This is usually a small note or todo list item, optionally with a due date. We may also want to include some extra context alongside the item, like the time it was captured and maybe even the meeting that I was in when I captured it (based on Google Calendar).

Each item is represented by a headline in an outline tree. The inbox is stored in one org file called "inbox.org"

#### Meeting capture

There should be an option to import items from a current or recent meeting, as shown by Google Calendar. The items should come from action items in meeting notes linked to the meeting.

### Inbox processing

Central to the GTD method is "processing" the inbox into a series of concrete tasks that can be performed. These tasks can either be freestanding or associated with a particular "project", which is just a fancy way of saying a goal with multiple steps.

The way this should work: the user selects an inbox item to process, and the TUI guides them through transforming it into one or more tasks, picking a project to add tasks to, creating a new project, etc

One enhancement beyond vanilla org-mode: we'd like the ability to associate tasks and projects with one or more Google calendar meeting IDs. This will help match items to current and upcoming meetings.

### Agenda view

Like org-mode, orgtd should support showing a list of due and upcoming items across all org files in the user's org directory. There are several things we care about in this view:
* Items that are overdue
* Items with upcoming due dates
* Items associated with a meeting currently in progress
* Items marked "NEXT" that are associated with projects with an upcoming meeting instance

### Review ("snippets mode")

By default we should hide items that were marked "DONE" more than a few days ago. But there should be a mode for viewing "DONE" items by completion date, so we can get a sense of what we accomplished on a given day, week, month, etc

## Guiding principles

* Fast: The TUI should never block or freeze, even if performing slow operations like fetching things over the network
* Modal TUI: Mimic vim keybindings, especially for movement between items ("kj"), promotion/demotion of headlines ("<<", ">>"), deletion ("dd"), insertion ("i", "a", "o", "O"). But rather than operating on characters and lines, the unit is an agenda item (which can span multiple lines). When editing individual items, perhaps give an option to drop into an external editor like vim.
  * There should also be a command mode (":")
* Google integration: there should be first-class support for pulling information from Google Calendar, linked Google Docs, Google Meet, etc. Note that integration *must* be read-only.
* org-mode compatible: Use the org file format. org-mode must be able to open the files we generate.
* org-centric: org files should be the only storage media. Some are meant to be edited by the user (like the inbox and tasks) while others are meant to be managed by the tool and only visible to the user (like the agenda view and calendar)
