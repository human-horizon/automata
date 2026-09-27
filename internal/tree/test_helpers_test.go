package tree

func startInputForTest(t *Tree, prompt string, done func(string)) {
	t.startCheckedInput(prompt, func(name string) error {
		done(name)
		return nil
	})
}

func addChildFolderForTest(t *Tree, parent *Item, name string) error {
	_, err := t.CreateChildFolder(parent, name)
	return err
}

func addChildChatForTest(t *Tree, parent *Item, name string) error {
	_, err := t.CreateChildChat(parent, name)
	return err
}
