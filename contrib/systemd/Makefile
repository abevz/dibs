.PHONY: install uninstall

SERVICE_FILE = dibsd.service
SERVICE_DIR = $(HOME)/.config/systemd/user
SYSTEMCTL_USER ?= sh ../install/systemctl-user.sh

install:
	@mkdir -p $(SERVICE_DIR) $(HOME)/.local/state/dibs $(HOME)/.local/share/dibs
	@chmod 700 $(HOME)/.local/state/dibs $(HOME)/.local/share/dibs
	cp $(SERVICE_FILE) $(SERVICE_DIR)/
	$(SYSTEMCTL_USER) daemon-reload
	@echo "Service installed. Run: $(SYSTEMCTL_USER) enable --now dibsd"

uninstall:
	-$(SYSTEMCTL_USER) stop dibsd
	-$(SYSTEMCTL_USER) disable dibsd
	rm -f $(SERVICE_DIR)/$(SERVICE_FILE)
	$(SYSTEMCTL_USER) daemon-reload
	@echo "Service uninstalled."
