# AI Agent Coding Guidelines - Mini-Lambda

Welcome, Agent. This document outlines the standards and principles for developing the **Mini-Lambda** codebase. Strict adherence ensures maintainability, scalability, and high-quality software.

---

## 1. Codebase Structure

The project follows the standard Go project layout:

- **`cmd/`**: Entry points for the application. Each subdirectory represents a binary (e.g., `cmd/server`, `cmd/build-worker`). Keep these files minimal—mostly initialization and orchestration.
- **`internal/`**: Private application code. High-level business logic and internal libraries.
  - `internal/api/`: Request handlers and routing.
  - `internal/domain/`: Domain models and business logic abstractions.
  - `internal/storage/`: Database and external storage implementations (SQL, S3).
  - `internal/workers/`: Background task processors.
- **`pkg/`**: Public libraries that can be shared across projects. Use only for truly generic utilities (e.g., hash functions, string manipulation).
- **`migrations/`**: SQL migration files for versioning the database schema.

---

## 2. SOLID Principles in Go

Apply SOLID principles to ensure the codebase remains flexible:

### **S: Single Responsibility Principle (SRP)**

- Each package should have a single purpose.
- Each struct/type should handle one part of the business logic.
- If a struct has too many dependencies, it's likely doing too much.

### **O: Open/Closed Principle (OCP)**

- Designs should be open for extension but closed for modification.
- Use **Interfaces** to allow swapping implementations without changing the core logic.

### **L: Liskov Substitution Principle (LSP)**

- If a function accepts an interface, any implementation of that interface must work correctly without the function needing to know which one it is.

### **I: Interface Segregation Principle (ISP)**

- Favor many small, specific interfaces over one large, "do-it-all" interface.
- _Go Proverb: "The bigger the interface, the weaker the abstraction."_

### **D: Dependency Inversion Principle (DIP)**

- Depend on abstractions (interfaces), not concrete implementations.
- Use **Constructor Injection** (e.g., `NewService(repo Repository)`) to pass dependencies.

---

## 3. The 25-Line Rule

Every method and function **must be less than 25 lines of code**.

### **How to Achieve This:**

1. **Extract Method**: Move complex logic inside a function to a private helper method.
2. **Early Returns**: Use "Happy Path" coding. Check for errors and return early to avoid deep nesting.
3. **Dedicated Handlers**: If a `switch` or `if/else` block is long, move the contents of each branch into its own function.
4. **Data Aggregation**: If a function takes too many arguments, use a configuration struct.

---

## 4. DRY (Don't Repeat Yourself)

Logic must never be duplicated.

- **Shared Domain**: Common logic should live in `internal/domain` or `pkg/`.
- **Composed Structs**: Use embedding and composition to reuse functionality across different types.
- **Utility Helpers**: If you find yourself writing the same string formatting or validation twice, move it to `pkg/utils`.

---

## 5. Coding Standards

- **Error Handling**: Always check errors immediately. Wrap errors to provide context: `fmt.Errorf("failed to process build: %w", err)`.
- **Naming**: Use camelCase for private variables and PascalCase for exported ones. Keep names descriptive but concise.
- **Concurrency**: Use channels and wait groups carefully. Prefer standard patterns like the Worker Pool or Fan-out/Fan-in.

---

## 6. Pro-Tip for Agents

Before submitting code:

1. Count the lines in your new functions.
2. Check if a similar utility already exists in `pkg/` or `internal/`.
3. Ensure you are injecting dependencies rather than creating them inside methods.
