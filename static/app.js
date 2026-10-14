function openWordEditor() {
  const dialog = document.querySelector('dialog[data-open-editor]');
  if (dialog && !dialog.open) {
    dialog.showModal();
    dialog.removeAttribute('data-open-editor');
  }
}
document.addEventListener('DOMContentLoaded', openWordEditor);
document.addEventListener('htmx:afterSwap', openWordEditor);
document.addEventListener('click', (event) => {
  const closeButton = event.target.closest('[data-close-editor]');
  if (closeButton) closeButton.closest('dialog').close();
});
