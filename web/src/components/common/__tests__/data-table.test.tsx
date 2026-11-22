import type { ColumnDef } from "@tanstack/react-table";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api";

import { DataTable } from "../data-table";

type Row = { name: string; n: number };
const columns: ColumnDef<Row, unknown>[] = [
  { accessorKey: "name", header: "Name" },
  { accessorKey: "n", header: "Count" },
];

describe("DataTable", () => {
  it("renders loading, empty and error states", async () => {
    const { rerender } = render(<DataTable columns={columns} data={undefined} loading />);
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();

    rerender(<DataTable columns={columns} data={[]} />);
    expect(screen.getByText("Nothing here yet")).toBeInTheDocument();

    const retry = vi.fn();
    rerender(<DataTable columns={columns} data={undefined} error={new ApiError(500, "internal_error", "boom")} onRetry={retry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    await userEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(retry).toHaveBeenCalled();
  });

  it("renders rows, sorts and handles row clicks", async () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} data={[{ name: "b", n: 2 }, { name: "a", n: 1 }]} onRowClick={onRowClick} />);
    const cellsBefore = screen.getAllByRole("cell").map((c) => c.textContent);
    expect(cellsBefore).toEqual(["b", "2", "a", "1"]);
    await userEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(screen.getAllByRole("cell").map((c) => c.textContent)).toEqual(["a", "1", "b", "2"]);
    await userEvent.click(screen.getByText("a"));
    expect(onRowClick).toHaveBeenCalledWith({ name: "a", n: 1 });
  });
});
