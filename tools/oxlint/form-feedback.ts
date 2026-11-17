import ts from 'typescript'

const feedback =
  /^(?:error|errors|success|failure|feedback|message)$|^(?:form|submit|submission|save|request|response|server|api).*(?:error|success|failure|message|feedback|result)$|^(?:error|success|failure)(?:message|text)$/iu
const formState = /^(?:form|submission|submit|response|request)$/iu

function reference(node: ts.Node): string[] {
  if (ts.isIdentifier(node)) return [node.text]
  if (ts.isPropertyAccessExpression(node)) return [...reference(node.expression), node.name.text]
  if (ts.isElementAccessExpression(node) && ts.isStringLiteralLike(node.argumentExpression))
    return [...reference(node.expression), node.argumentExpression.text]
  return []
}

export function formFeedback(script: string) {
  const variables = new Map<string, ts.Expression>()
  const file = ts.createSourceFile('component.ts', script, ts.ScriptTarget.Latest, true)
  function collect(node: ts.Node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer)
      variables.set(node.name.text, node.initializer)
    ts.forEachChild(node, collect)
  }
  collect(file)
  function detects(node: ts.Node, field: boolean, seen = new Set<string>()): boolean {
    if (ts.isPropertyAssignment(node)) return detects(node.initializer, field, seen)
    const path = reference(node)
    if (path.length) {
      const name = path.at(-1)!.replaceAll('_', '')
      if (field && path[0] === 'field' && path.length === 2) return false
      if (feedback.test(name) && !(path.length > 1 && ['event', 'row', 'log'].includes(path[0])))
        return true
      if (path.some((part) => formState.test(part)) && /^(?:result|status)$/iu.test(name))
        return true
      const value = variables.get(path[0])
      if (value && !seen.has(path[0])) return detects(value, field, new Set([...seen, path[0]]))
      return false
    }
    if (ts.isCallExpression(node) && node.expression.getText() === 'msg')
      return node.arguments.some(
        (arg) =>
          ts.isStringLiteralLike(arg) &&
          /^(?:toast\.|error\.|form\.(?:success|error))/u.test(arg.text),
      )
    return ts.forEachChild(node, (child) => detects(child, field, seen) || undefined) ?? false
  }
  return (source: string, field: boolean): boolean => {
    const parsed = ts.createSourceFile('feedback.ts', `(${source})`, ts.ScriptTarget.Latest, true)
    return detects(parsed, field)
  }
}
