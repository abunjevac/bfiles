package keymap

// Action is a named user action. Its value is the snake_case key used in keymap.toml.
type Action string

// Category groups actions in the help overlay.
type Category string

// Help categories, in display order.
const (
	CatNavigate Category = "Navigate"
	CatSelect   Category = "Select"
	CatFile     Category = "Files"
	CatArchive  Category = "Archives"
	CatView     Category = "View"
	CatRemote   Category = "Remote"
	CatApp      Category = "App"
)

// Categories lists help categories in display order.
func Categories() []Category {
	return []Category{CatNavigate, CatSelect, CatFile, CatArchive, CatView, CatRemote, CatApp}
}

// Known actions.
const (
	CursorUp     Action = "cursor_up"
	CursorDown   Action = "cursor_down"
	PageUp       Action = "page_up"
	PageDown     Action = "page_down"
	Top          Action = "top"
	Bottom       Action = "bottom"
	Parent       Action = "parent"
	Enter        Action = "enter"
	SwitchPanel  Action = "switch_panel"
	Back         Action = "back"
	Forward      Action = "forward"
	HomeDir      Action = "home_dir"
	GotoPath     Action = "goto_path"
	Bookmarks    Action = "bookmarks"
	SameLocation Action = "same_location"
	SwapPanels   Action = "swap_panels"
	Reload       Action = "reload"

	ToggleSelect    Action = "toggle_select"
	SelectUp        Action = "select_up"
	SelectDown      Action = "select_down"
	SelectAll       Action = "select_all"
	InvertSelection Action = "invert_selection"
	SelectPattern   Action = "select_pattern"
	UnselectPattern Action = "unselect_pattern"
	ClearSelection  Action = "clear_selection"

	Copy     Action = "copy"
	Move     Action = "move"
	Rename   Action = "rename"
	Mkdir    Action = "mkdir"
	NewFile  Action = "new_file"
	Delete   Action = "delete"
	Edit     Action = "edit"
	Open     Action = "open"
	Chmod    Action = "chmod"
	CopyPath Action = "copy_path"
	DirSize  Action = "dir_size"
	SizeAll  Action = "dir_size_all"
	Search   Action = "search"
	FileInfo Action = "info"

	Pack    Action = "pack"
	Extract Action = "extract"

	Filter        Action = "filter"
	ToggleHidden  Action = "toggle_hidden"
	ToggleSpecial Action = "toggle_special"
	ToggleMtime   Action = "toggle_mtime"
	ToggleBytes   Action = "toggle_bytes"
	SortName      Action = "sort_name"
	SortSize      Action = "sort_size"
	SortMtime     Action = "sort_mtime"
	SortExt       Action = "sort_ext"
	SortReverse   Action = "sort_reverse"
	QuickView     Action = "quick_view"
	View          Action = "view"

	Connect    Action = "connect"
	Disconnect Action = "disconnect"
	Terminal   Action = "terminal"
	Explorer   Action = "explorer"

	Queue      Action = "queue"
	Help       Action = "help"
	Theme      Action = "theme"
	Settings   Action = "settings"
	EditConfig Action = "edit_config"
	Quit       Action = "quit"
)

// Info describes an action for which-key and the help overlay.
type Info struct {
	Action   Action
	Category Category
	Label    string
}

//nolint:gochecknoglobals // static table
var infos = []Info{
	{CursorUp, CatNavigate, "cursor up"},
	{CursorDown, CatNavigate, "cursor down"},
	{PageUp, CatNavigate, "page up"},
	{PageDown, CatNavigate, "page down"},
	{Top, CatNavigate, "first entry"},
	{Bottom, CatNavigate, "last entry"},
	{Parent, CatNavigate, "parent dir"},
	{Enter, CatNavigate, "enter / open"},
	{SwitchPanel, CatNavigate, "switch panel"},
	{Back, CatNavigate, "history back"},
	{Forward, CatNavigate, "history forward"},
	{HomeDir, CatNavigate, "home dir"},
	{GotoPath, CatNavigate, "go to path"},
	{Bookmarks, CatNavigate, "bookmarks"},
	{SameLocation, CatNavigate, "other panel here"},
	{SwapPanels, CatNavigate, "swap panels"},
	{Reload, CatNavigate, "reload"},

	{ToggleSelect, CatSelect, "toggle selection"},
	{SelectUp, CatSelect, "select up"},
	{SelectDown, CatSelect, "select down"},
	{SelectAll, CatSelect, "select all"},
	{InvertSelection, CatSelect, "invert selection"},
	{SelectPattern, CatSelect, "select by pattern"},
	{UnselectPattern, CatSelect, "unselect by pattern"},
	{ClearSelection, CatSelect, "clear selection / filter"},

	{Copy, CatFile, "copy"},
	{Move, CatFile, "move"},
	{Rename, CatFile, "rename"},
	{Mkdir, CatFile, "new dir"},
	{NewFile, CatFile, "new file"},
	{Delete, CatFile, "delete"},
	{Edit, CatFile, "edit"},
	{Open, CatFile, "open with app"},
	{Chmod, CatFile, "permissions"},
	{CopyPath, CatFile, "copy path"},
	{DirSize, CatFile, "dir size"},
	{SizeAll, CatFile, "all dir sizes"},
	{Search, CatFile, "search"},
	{FileInfo, CatFile, "file info"},

	{Pack, CatArchive, "pack"},
	{Extract, CatArchive, "extract"},

	{Filter, CatView, "filter"},
	{ToggleHidden, CatView, "hidden files"},
	{ToggleSpecial, CatView, "special dirs"},
	{ToggleMtime, CatView, "mtime column"},
	{ToggleBytes, CatView, "exact byte sizes"},
	{SortName, CatView, "sort by name"},
	{SortSize, CatView, "sort by size"},
	{SortMtime, CatView, "sort by time"},
	{SortExt, CatView, "sort by ext"},
	{SortReverse, CatView, "reverse sort"},
	{QuickView, CatView, "quick view"},
	{View, CatView, "viewer"},

	{Connect, CatRemote, "ssh connect"},
	{Disconnect, CatRemote, "close ssh / archive / search"},
	{Terminal, CatRemote, "terminal here"},
	{Explorer, CatRemote, "file explorer here"},

	{Queue, CatApp, "job queue"},
	{Help, CatApp, "help"},
	{Theme, CatApp, "theme"},
	{Settings, CatApp, "settings"},
	{EditConfig, CatApp, "edit config file"},
	{Quit, CatApp, "quit"},
}

//nolint:gochecknoglobals // built once from infos
var infoByAction = func() map[Action]Info {
	m := make(map[Action]Info, len(infos))

	for _, i := range infos {
		m[i.Action] = i
	}

	return m
}()

// Lookup returns metadata for a known action.
func Lookup(a Action) (Info, bool) {
	i, ok := infoByAction[a]

	return i, ok
}

// All returns every known action in help order.
func All() []Info {
	return infos
}
